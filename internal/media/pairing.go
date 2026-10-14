package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"golang.org/x/net/websocket"
)

func (c *Client) samsungSocket(ctx context.Context, d Device, credential Credential, endpoint string) (*websocket.Conn, string, error) {
	if d.Validate() != nil || d.Kind != "samsung" {
		return nil, "", errors.New("Invalid Samsung target")
	}
	conn, pin, e := c.deviceTLS(ctx, net.JoinHostPort(d.Host, "8002"), credential.SamsungPin)
	if e != nil {
		return nil, "", e
	}
	// Pairing may wait for a person to approve the TV prompt. Normal commands and
	// reads remain bounded by their shorter caller context.
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	address := "wss://" + d.Host + ":8002/api/v2/channels/" + endpoint
	q := url.Values{"name": {base64.StdEncoding.EncodeToString([]byte("Home HQ"))}}
	if credential.SamsungToken != "" {
		q.Set("token", credential.SamsungToken)
	}
	config, e := websocket.NewConfig(address+"?"+q.Encode(), "http://"+d.Host)
	if e != nil {
		conn.Close()
		return nil, "", e
	}
	ws, e := websocket.NewClient(config, conn)
	if e != nil {
		conn.Close()
		return nil, "", errors.New("TV connection was not accepted")
	}
	ws.MaxPayloadBytes = 65536
	return ws, pin, nil
}
func (c *Client) PairSamsung(ctx context.Context, d Device, old Credential) (Credential, error) {
	old.SamsungToken = ""
	ws, pin, e := c.samsungSocket(ctx, d, old, "samsung.remote.control")
	if e != nil {
		return Credential{}, e
	}
	defer ws.Close()
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	defer stop()
	for i := 0; i < 16; i++ {
		var response struct {
			Event string
			Data  struct{ Token string }
		}
		if e = websocket.JSON.Receive(ws, &response); e != nil {
			return Credential{}, errors.New("Pairing timed out or was declined on the TV")
		}
		if response.Event == "ms.channel.unauthorized" {
			return Credential{}, ErrAuth
		}
		if response.Event == "ms.channel.connect" && regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`).MatchString(response.Data.Token) {
			old.Host = d.Host
			old.SamsungToken = response.Data.Token
			old.SamsungPin = pin
			return old, nil
		}
	}
	return Credential{}, ErrAuth
}
func (c *Client) PairSony(ctx context.Context, d Device, old Credential, key string) (Credential, error) {
	if d.Kind != "sony" || len(key) < 1 || len(key) > 128 {
		return Credential{}, errors.New("Enter the TV's pre-shared key")
	}
	old.Host = d.Host
	old.SonyPSK = key
	if _, e := c.sony(ctx, d, old, "system", "getSystemInformation", nil); e != nil {
		return Credential{}, errors.New("The TV did not accept this key")
	}
	return old, nil
}
func (c *Client) readSamsungArt(ctx context.Context, d Device, credential Credential) (bool, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ws, _, e := c.samsungSocket(ctx, d, credential, "com.samsung.art-app")
	if e != nil {
		return false, false
	}
	defer ws.Close()
	stop := context.AfterFunc(ctx, func() { ws.Close() })
	defer stop()
	var uuid [16]byte
	if _, e := rand.Read(uuid[:]); e != nil {
		return false, false
	}
	uuid[6] = uuid[6]&15 | 64
	uuid[8] = uuid[8]&63 | 128
	id := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
	payload, _ := json.Marshal(map[string]string{"request": "get_artmode_status", "id": id})
	message := map[string]any{"method": "ms.channel.emit", "params": map[string]any{"event": "art_app_request", "to": "host", "data": string(payload)}}
	if websocket.JSON.Send(ws, message) != nil {
		return false, false
	}
	for i := 0; i < 12; i++ {
		var response struct {
			Event string
			Data  json.RawMessage
		}
		if websocket.JSON.Receive(ws, &response) != nil {
			break
		}
		if response.Event != "d2d_service_message" {
			continue
		}
		var data string
		if json.Unmarshal(response.Data, &data) != nil {
			continue
		}
		var status struct{ Event, Value string }
		if json.Unmarshal([]byte(data), &status) == nil && status.Event == "artmode_status" {
			if status.Value == "on" {
				return true, true
			}
			if status.Value == "off" {
				return true, false
			}
		}
	}
	return false, false
}
func ValidCommand(command string) bool {
	switch command {
	case "play", "pause", "volume-up", "volume-down", "mute":
		return true
	}
	return false
}
func (c *Client) Command(ctx context.Context, d Device, credential Credential, command string) error {
	if !ValidCommand(command) || !credential.Paired(d) || d.Validate() != nil {
		return errors.New("Pair this TV before sending an allowed media command")
	}
	if d.Kind == "samsung" {
		keys := map[string]string{"play": "KEY_PLAY", "pause": "KEY_PAUSE", "volume-up": "KEY_VOLUP", "volume-down": "KEY_VOLDOWN", "mute": "KEY_MUTE"}
		ws, _, e := c.samsungSocket(ctx, d, credential, "samsung.remote.control")
		if e != nil {
			return e
		}
		defer ws.Close()
		stop := context.AfterFunc(ctx, func() { ws.Close() })
		defer stop()
		accepted := false
		for i := 0; i < 12; i++ {
			var response struct{ Event string }
			if e = websocket.JSON.Receive(ws, &response); e != nil {
				return errors.New("TV did not authorize this connection")
			}
			if response.Event == "ms.channel.unauthorized" {
				return ErrAuth
			}
			if response.Event == "ms.channel.connect" {
				accepted = true
				break
			}
		}
		if !accepted {
			return ErrAuth
		}
		return websocket.JSON.Send(ws, map[string]any{"method": "ms.remote.control", "params": map[string]string{"Cmd": "Click", "DataOfCmd": keys[command], "Option": "false", "TypeOfRemote": "SendRemoteKey"}})
	}
	if command == "volume-up" || command == "volume-down" {
		level := "+1"
		if command == "volume-down" {
			level = "-1"
		}
		_, e := c.sony(ctx, d, credential, "audio", "setAudioVolume", []any{map[string]string{"target": "speaker", "volume": level}})
		return e
	}
	if command == "mute" {
		r, e := c.sony(ctx, d, credential, "audio", "getVolumeInformation", nil)
		if e != nil {
			return e
		}
		var volumes []struct {
			Target string
			Mute   bool
		}
		if json.Unmarshal(r[0], &volumes) != nil {
			return errors.New("Volume state unavailable")
		}
		for _, v := range volumes {
			if v.Target == "speaker" {
				_, e = c.sony(ctx, d, credential, "audio", "setAudioMute", []any{map[string]bool{"status": !v.Mute}})
				return e
			}
		}
		return errors.New("Speaker mute state unavailable")
	}
	// Fetch the TV's own advertised IRCC code for a fixed Play/Pause command.
	body, _ := json.Marshal(map[string]any{"method": "getRemoteControllerInfo", "params": []any{}, "id": 1, "version": "1.0"})
	var reply struct{ Result []json.RawMessage }
	if e := c.request(ctx, d, "POST", "http://"+d.Host+"/sony/system", body, credential.SonyPSK, &reply); e != nil {
		return e
	}
	if len(reply.Result) < 2 {
		return errors.New("Remote commands unavailable")
	}
	var commands []struct{ Name, Value string }
	if json.Unmarshal(reply.Result[1], &commands) != nil {
		return errors.New("Invalid TV command list")
	}
	wanted := "Play"
	if command == "pause" {
		wanted = "Pause"
	}
	code := ""
	for _, key := range commands {
		if key.Name == wanted && len(key.Value) <= 128 {
			code = key.Value
			break
		}
	}
	if code == "" {
		return errors.New("TV does not expose this playback command")
	}
	var escaped bytes.Buffer
	xml.EscapeText(&escaped, []byte(code))
	soap := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:X_SendIRCC xmlns:u="urn:schemas-sony-com:service:IRCC:1"><IRCCCode>` + escaped.String() + `</IRCCCode></u:X_SendIRCC></s:Body></s:Envelope>`
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+d.Host+"/sony/IRCC", bytes.NewBufferString(soap))
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPACTION", `"urn:schemas-sony-com:service:IRCC:1#X_SendIRCC"`)
	req.Header.Set("X-Auth-PSK", credential.SonyPSK)
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 16384))
	if resp.StatusCode != 200 {
		return errors.New("TV did not accept the playback command")
	}
	return nil
}
