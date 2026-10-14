package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("Spotify authorization expired; reconnect your account")
var ErrRestricted = errors.New("Spotify declined this request. Check app user access, Premium and device restrictions")
var ErrNoPlayer = errors.New("No active Spotify player; start music in Spotify first")

type RateError struct{ Delay time.Duration }

func (e RateError) Error() string { return "Spotify rate limit reached; waiting before retrying" }

type Client struct {
	HTTP                  *http.Client
	API, Token, Authorize string
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Spotify redirects are not followed") }}, API: "https://api.spotify.com/v1", Token: "https://accounts.spotify.com/api/token", Authorize: "https://accounts.spotify.com/authorize"}
}
func (c *Client) AuthURL(cfg Config, p Pending) string {
	q := url.Values{"client_id": {cfg.ClientID}, "response_type": {"code"}, "redirect_uri": {cfg.Redirect}, "scope": {Scopes}, "code_challenge_method": {"S256"}, "code_challenge": {Challenge(p.Verifier)}}
	return c.Authorize + "?" + q.Encode()
}
func (c *Client) AuthorizationURL(cfg Config, p Pending, state string) string {
	return c.AuthURL(cfg, p) + "&state=" + url.QueryEscape(state)
}
func (c *Client) request(ctx context.Context, method, address, token, content string, body []byte, target any) error {
	req, e := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if e != nil {
		return errors.New("Invalid Spotify request")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if content != "" {
		req.Header.Set("Content-Type", content)
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return errors.New("Spotify is unavailable; retry shortly")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 401:
		return ErrUnauthorized
	case 403:
		return ErrRestricted
	case 404:
		return ErrNoPlayer
	case 429:
		n, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return RateError{Delay: time.Duration(min(3600, max(30, n))) * time.Second}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("Spotify did not accept the request")
	}
	if resp.StatusCode == 204 || target == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 512*1024+1))
	if e != nil || len(b) > 512*1024 {
		return errors.New("Spotify response was too large or incomplete")
	}
	if json.Unmarshal(b, target) != nil {
		return errors.New("Invalid Spotify response")
	}
	return nil
}
func (c *Client) token(ctx context.Context, values url.Values) (Credential, error) {
	var response struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Expires      int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	b := []byte(values.Encode())
	if e := c.request(ctx, "POST", c.Token, "", "application/x-www-form-urlencoded", b, &response); e != nil {
		return Credential{}, e
	}
	if response.AccessToken == "" || len(response.AccessToken) > 4096 || len(response.RefreshToken) > 4096 || !strings.EqualFold(response.TokenType, "Bearer") || response.Expires <= 0 || response.Expires > 86400 {
		return Credential{}, ErrUnauthorized
	}
	return Credential{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, Scope: response.Scope, Expires: time.Now().Add(time.Duration(response.Expires) * time.Second)}, nil
}
func (c *Client) Exchange(ctx context.Context, p Pending, code string) (Credential, error) {
	if code == "" || len(code) > 4096 {
		return Credential{}, ErrUnauthorized
	}
	credential, e := c.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {p.Redirect}, "client_id": {p.ClientID}, "code_verifier": {p.Verifier}})
	if e != nil {
		return credential, e
	}
	if credential.RefreshToken == "" {
		return Credential{}, ErrUnauthorized
	}
	credential.ClientID = p.ClientID
	credential.Revision = Random()
	var profile struct {
		ID   string
		Name string `json:"display_name"`
	}
	if e = c.request(ctx, "GET", c.API+"/me", credential.AccessToken, "", nil, &profile); e != nil {
		return Credential{}, e
	}
	credential.AccountID = Text(profile.ID, 100)
	if credential.AccountID == "" {
		return Credential{}, ErrUnauthorized
	}
	credential.AccountName = Text(profile.Name, 80)
	if credential.AccountName == "" {
		credential.AccountName = credential.AccountID
	}
	return credential, nil
}
func (c *Client) Refresh(ctx context.Context, old Credential) (Credential, error) {
	credential, e := c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {old.RefreshToken}, "client_id": {old.ClientID}})
	if e != nil {
		return Credential{}, e
	}
	if credential.RefreshToken == "" {
		credential.RefreshToken = old.RefreshToken
	}
	if credential.Scope == "" {
		credential.Scope = old.Scope
	}
	credential.ClientID = old.ClientID
	credential.AccountID = old.AccountID
	credential.AccountName = old.AccountName
	credential.Revision = Random()
	return credential, nil
}

type apiDevice struct {
	ID, Name, Type string
	Active         bool `json:"is_active"`
	Restricted     bool `json:"is_restricted"`
	SupportsVolume bool `json:"supports_volume"`
	Volume         *int `json:"volume_percent"`
}

func (d apiDevice) device() Device {
	return Device{ID: d.ID, Name: Text(d.Name, 80), Type: Text(d.Type, 40), Active: d.Active, Restricted: d.Restricted, SupportsVolume: d.SupportsVolume, Volume: d.Volume}
}
func (c *Client) Playback(ctx context.Context, credential Credential) (Snapshot, error) {
	var response struct {
		Device   apiDevice
		Playing  bool   `json:"is_playing"`
		Progress int64  `json:"progress_ms"`
		ItemType string `json:"currently_playing_type"`
		Actions  struct{ Disallows map[string]bool }
		Item     *struct {
			Name     string
			Duration int64 `json:"duration_ms"`
			Artists  []struct{ Name string }
			Album    struct {
				Name   string
				Images []struct{ URL string }
			}
			Images []struct{ URL string }
			Show   struct {
				Name, Publisher string
				Images          []struct{ URL string }
			}
			ExternalURLs map[string]string `json:"external_urls"`
		}
	}
	if e := c.request(ctx, "GET", c.API+"/me/player?additional_types=track,episode", credential.AccessToken, "", nil, &response); e != nil {
		return Snapshot{}, e
	}
	s := Snapshot{Device: response.Device.device(), Playing: response.Playing, Progress: response.Progress, Fetched: time.Now(), ItemType: Text(response.ItemType, 30), Disallows: response.Actions.Disallows}
	if i := response.Item; i != nil {
		s.Title = Text(i.Name, 160)
		s.Duration = i.Duration
		s.Album = Text(i.Album.Name, 160)
		names := []string{}
		for _, a := range i.Artists[:min(16, len(i.Artists))] {
			names = append(names, Text(a.Name, 80))
		}
		s.Artists = Text(strings.Join(names, ", "), 160)
		if response.ItemType == "episode" {
			s.Artists = Text(i.Show.Publisher, 160)
			s.Album = Text(i.Show.Name, 160)
		}
		s.ItemURL = i.ExternalURLs["spotify"]
		for _, images := range [][]struct{ URL string }{i.Album.Images, i.Images, i.Show.Images} {
			for _, image := range images {
				if AllowedImage(image.URL) {
					s.ImageURL = image.URL
					break
				}
			}
			if s.ImageURL != "" {
				break
			}
		}
		if s.ImageURL != "" {
			s.ImageKey = Hash(s.ImageURL)[:24]
		}
	}
	return s, s.Validate()
}
func (c *Client) Devices(ctx context.Context, credential Credential) ([]Device, error) {
	var reply struct{ Devices []apiDevice }
	if e := c.request(ctx, "GET", c.API+"/me/player/devices", credential.AccessToken, "", nil, &reply); e != nil {
		return nil, e
	}
	if len(reply.Devices) > 64 {
		return nil, errors.New("Too many Spotify devices")
	}
	ds := []Device{}
	for _, d := range reply.Devices {
		device := d.device()
		if device.ID == "" {
			continue
		}
		if !device.Valid() {
			return nil, errors.New("Invalid Spotify device")
		}
		ds = append(ds, device)
	}
	return ds, nil
}
func (c *Client) Command(ctx context.Context, credential Credential, d Device, action string) error {
	if !d.Valid() || d.Restricted {
		return ErrRestricted
	}
	q := url.Values{"device_id": {d.ID}}
	path := ""
	method := "PUT"
	var body []byte
	switch action {
	case "play", "pause":
		path = "/me/player/" + action
	case "next", "previous":
		method = "POST"
		path = "/me/player/" + action
	case "volume-up", "volume-down":
		if !d.SupportsVolume || d.Volume == nil {
			return errors.New("Spotify cannot change volume on this device")
		}
		level := *d.Volume + 5
		if action == "volume-down" {
			level = *d.Volume - 5
		}
		q.Set("volume_percent", strconv.Itoa(min(100, max(0, level))))
		path = "/me/player/volume"
	case "transfer":
		path = "/me/player"
		q = url.Values{}
		body, _ = json.Marshal(map[string]any{"device_ids": []string{d.ID}, "play": false})
	default:
		return errors.New("Unsupported Spotify command")
	}
	return c.request(ctx, method, c.API+path+"?"+q.Encode(), credential.AccessToken, "application/json", body, nil)
}
