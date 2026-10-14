package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

var ErrAuth = errors.New("TV authorization required")
var ErrStandby = errors.New("TV is in standby")

type Client struct {
	HTTP *http.Client
	Dial func(context.Context, string, string) (net.Conn, error)
}

func NewClient() *Client {
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 3 * time.Second, MaxIdleConns: 8, IdleConnTimeout: 30 * time.Second}
	return &Client{HTTP: &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("TV redirects are not followed") }}, Dial: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
}
func (c *Client) request(ctx context.Context, d Device, method, address string, body []byte, psk string, target any) error {
	if d.Validate() != nil {
		return errors.New("Invalid media target")
	}
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Hostname() != d.Host || u.Fragment != "" || (u.Port() != "" && u.Port() != "8001") {
		return errors.New("Invalid TV endpoint")
	}
	req, e := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if e != nil {
		return e
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if psk != "" {
		req.Header.Set("X-Auth-PSK", psk)
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return ErrAuth
	}
	if resp.StatusCode != 200 {
		return errors.New("TV did not return a successful response")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 128*1024+1))
	if e != nil {
		return e
	}
	if len(b) > 128*1024 {
		return errors.New("TV response too large")
	}
	return json.Unmarshal(b, target)
}
func (c *Client) sony(ctx context.Context, d Device, credential Credential, service, method string, params any) ([]json.RawMessage, error) {
	// Only a caller-specified private IP and fixed Sony service paths are used.
	switch service {
	case "system", "audio", "avContent":
	default:
		return nil, errors.New("Unsupported Sony service")
	}
	if params == nil {
		params = []any{}
	}
	body, _ := json.Marshal(map[string]any{"method": method, "params": params, "id": 1, "version": "1.0"})
	var response struct {
		Result []json.RawMessage
		Error  []json.RawMessage
	}
	psk := ""
	if credential.Host == d.Host {
		psk = credential.SonyPSK
	}
	if e := c.request(ctx, d, "POST", "http://"+d.Host+"/sony/"+service, body, psk, &response); e != nil {
		return nil, e
	}
	if len(response.Error) > 0 {
		var code int
		json.Unmarshal(response.Error[0], &code)
		if code == 401 || code == 403 {
			return nil, ErrAuth
		}
		if code == 40005 {
			return nil, ErrStandby
		}
		return nil, errors.New("TV status not available")
	}
	if len(response.Result) == 0 {
		return nil, errors.New("Missing TV status result")
	}
	return response.Result, nil
}
func (c *Client) Read(ctx context.Context, d Device, credential Credential) (Snapshot, error) {
	if e := d.Validate(); e != nil {
		return Snapshot{}, e
	}
	status := Snapshot{Updated: time.Now()}
	if d.Kind == "sony" {
		result, e := c.sony(ctx, d, credential, "system", "getPowerStatus", nil)
		if e == nil {
			var power struct{ Status string }
			if json.Unmarshal(result[0], &power) == nil {
				if power.Status == "active" {
					status.Power = "on"
				}
				if power.Status == "standby" {
					status.Power = "standby"
				}
				status.Reachable = true
				status.Native = true
			}
		}
		status.AuthRequired = errors.Is(e, ErrAuth)
		// Network presence and stale Cast app advertisements do not establish playback.
		if status.Power == "standby" {
			status.Note = "The TV reports standby. Its network service is still available."
			return status, nil
		}
		if credential.Paired(d) && status.Reachable {
			if r, e := c.sony(ctx, d, credential, "audio", "getVolumeInformation", nil); e == nil {
				var volumes []struct {
					Target string
					Volume int
					Mute   bool
				}
				if json.Unmarshal(r[0], &volumes) == nil {
					for _, v := range volumes {
						if v.Target == "speaker" && v.Volume >= 0 && v.Volume <= 100 {
							level, muted := v.Volume, v.Mute
							status.Volume = &level
							status.Muted = &muted
						}
					}
				}
			}
		}
	}
	if d.Kind == "samsung" {
		var reply struct {
			Device struct {
				PowerState, ModelName string
				FrameTVSupport        json.RawMessage
			}
		}
		if e := c.request(ctx, d, "GET", "http://"+d.Host+":8001/api/v2/", nil, "", &reply); e == nil && reply.Device.ModelName != "" {
			status.Reachable = true
			status.Native = true
			switch reply.Device.PowerState {
			case "on":
				status.Power = "on"
			case "off", "standby":
				status.Power = "standby"
			}
			if status.Power == "standby" {
				return status, nil
			}
			if credential.Paired(d) {
				known, art := c.readSamsungArt(ctx, d, credential)
				status.ArtKnown = known
				status.ArtMode = art
			}
			if status.ArtMode {
				status.Note = "The TV is displaying artwork."
				return status, nil
			}
			status.Note = "The TV is reachable. Current app or Art Mode is not exposed by its public status endpoint."
		}
	}
	if d.Cast {
		cast, e := c.readCast(ctx, d, credential.CastPin)
		if e == nil {
			status.Reachable = true
			status.App = cast.App
			status.Title = cast.Title
			status.Artist = cast.Artist
			status.Album = cast.Album
			status.Playback = cast.Playback
			status.Position = cast.Position
			status.Duration = cast.Duration
			status.CastPin = cast.CastPin
			if status.Volume == nil {
				status.Volume = cast.Volume
				status.Muted = cast.Muted
			}
			status.Note = cast.Note
		}
	}
	if !status.Reachable {
		return status, errors.New("No response; the device may be off or unavailable")
	}
	if status.Title == "" && status.Note == "" {
		status.Note = "This app is not reporting playback details."
	}
	return status, status.Validate()
}

// Probe only the public description/status endpoints. It never pairs or controls.
func (c *Client) Probe(ctx context.Context, host, kind, name string) (Device, error) {
	if !PrivateHost(host) {
		return Device{}, errors.New("Enter a private IPv4 address, such as 192.168.1.50")
	}
	d := Device{Host: host, Kind: kind, Name: Text(name, 60), UID: "manual:" + kind + ":" + host, Enabled: true, CustomName: true, Cast: kind == "cast", Seen: time.Now()}
	d.ID = ID(d.UID)
	if e := d.Validate(); e != nil {
		return d, e
	}
	if kind == "samsung" {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+host+":9197/dmr", nil)
		resp, e := c.HTTP.Do(req)
		if e == nil {
			b, e := io.ReadAll(io.LimitReader(resp.Body, 128*1024+1))
			resp.Body.Close()
			if e == nil && resp.StatusCode == 200 && len(b) <= 128*1024 {
				found, e := parseDescription(b, host)
				if e == nil && found.Kind == "samsung" {
					found.Name = d.Name
					found.CustomName = true
					return found, nil
				}
			}
		}
	}
	return d, nil
}
