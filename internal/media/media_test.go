package media

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"golang.org/x/net/websocket"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func device(kind string) Device {
	d := Device{UID: "test:" + kind, Host: "192.168.1.20", Kind: kind, Name: "TV", Enabled: true}
	d.ID = ID(d.UID)
	return d
}
func reply(body string, code int) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
func TestDescriptionAndPrivateTargets(t *testing.T) {
	b := []byte(`<root><device><manufacturer>Samsung Electronics</manufacturer><friendlyName>Family &amp; TV</friendlyName><modelName>QN75LS03A</modelName><UDN>uuid:stable</UDN></device></root>`)
	d, e := parseDescription(b, "192.168.1.25")
	if e != nil || d.Kind != "samsung" || d.Name != "Family & TV" || d.ID != ID("upnp:uuid:stable") {
		t.Fatal(d, e)
	}
	for _, h := range []string{"127.0.0.1", "169.254.169.254", "8.8.8.8", "[::1]", "192.168.1.1:80", "router.local", "192.168.1.20/path"} {
		if PrivateHost(h) {
			t.Fatal("unsafe host", h)
		}
	}
	c := NewClient()
	calls := 0
	c.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { calls++; return reply(`{}`, 200), nil })
	for _, address := range []string{"https://192.168.1.20/", "http://192.168.1.21/", "http://user@192.168.1.20/", "http://192.168.1.20:22/"} {
		if c.request(context.Background(), device("sony"), "GET", address, nil, "", &struct{}{}) == nil {
			t.Fatal(address)
		}
	}
	if calls != 0 {
		t.Fatal("sent request to unapproved target")
	}
}
func TestSonyStandbyAndCredentialHostBinding(t *testing.T) {
	d := device("sony")
	d.Cast = true
	c := NewClient()
	calls := 0
	c.Dial = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("Cast queried in standby")
		return nil, errors.New("unexpected")
	}
	c.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("X-Auth-PSK") != "" {
			t.Fatal("key followed a DHCP change")
		}
		var body struct{ Method string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.Method != "getPowerStatus" {
			t.Fatal(body)
		}
		return reply(`{"result":[{"status":"standby"}]}`, 200), nil
	})
	status, e := c.Read(context.Background(), d, Credential{Host: "192.168.1.99", SonyPSK: "private-key"})
	if e != nil || status.Power != "standby" || status.Title != "" || calls != 1 {
		t.Fatal(status, e, calls)
	}
	for _, action := range []string{"power", "KEY_POWER", "launch", "http://evil", "volume=100"} {
		if c.Command(context.Background(), d, Credential{Host: d.Host, SonyPSK: "key"}, action) == nil {
			t.Fatal("accepted", action)
		}
	}
	if calls != 1 {
		t.Fatal("unsupported commands reached TV")
	}
}
func TestSamsungUnpairedReadDoesNotPrompt(t *testing.T) {
	c := NewClient()
	c.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v2/" || r.Method != "GET" {
			t.Fatal(r)
		}
		return reply(`{"device":{"PowerState":"on","ModelName":"QN75LS03A"}}`, 200), nil
	})
	c.Dial = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("unpaired read opened WebSocket")
		return nil, errors.New("unexpected")
	}
	s, e := c.Read(context.Background(), device("samsung"), Credential{})
	if e != nil || s.Power != "on" || s.ArtKnown || s.Title != "" {
		t.Fatal(s, e)
	}
}
func TestWireBounds(t *testing.T) {
	for _, b := range [][]byte{{0x22, 0xff}, {0x80}, {0x22, 0x10, 1}, {7, 0}} {
		if _, _, e := decodeCast(b); e == nil {
			t.Fatal("invalid envelope accepted", b)
		}
	}
	wire := encodeCast(castMedia, "receiver-0", map[string]string{"type": "GET_STATUS"})
	ns, p, e := decodeCast(wire)
	if e != nil || ns != castMedia || !strings.Contains(string(p), "GET_STATUS") {
		t.Fatal(ns, string(p), e)
	}
}
func testTLS(t *testing.T) (net.Listener, *Client) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	ln, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ln.Close() })
	c := NewClient()
	c.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
	}
	return ln, c
}
func TestCastStatusReadOnlyAndPinChange(t *testing.T) {
	ln, c := testTLS(t)
	done := make(chan error, 1)
	go func() {
		conn, e := ln.Accept()
		if e != nil {
			done <- e
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		for i := 0; i < 2; i++ {
			ns, p, e := receiveCast(conn)
			if e != nil {
				done <- e
				return
			}
			if i == 0 && ns != castConnection || i == 1 && ns != castReceiver {
				done <- errors.New("unexpected namespace")
				return
			}
			if strings.Contains(string(p), "LAUNCH") {
				done <- errors.New("launch sent")
				return
			}
		}
		sendCast(conn, castReceiver, "home-hq", map[string]any{"type": "RECEIVER_STATUS", "status": map[string]any{"applications": []any{map[string]any{"displayName": "Player", "transportId": "app-1", "namespaces": []any{map[string]string{"name": castMedia}}}}, "volume": map[string]any{"level": 0.3, "muted": false}}})
		for i := 0; i < 2; i++ {
			_, p, e := receiveCast(conn)
			if e != nil {
				done <- e
				return
			}
			var v struct{ Type string }
			json.Unmarshal(p, &v)
			if v.Type != "CONNECT" && v.Type != "GET_STATUS" {
				done <- errors.New("mutating Cast request")
				return
			}
		}
		sendCast(conn, castMedia, "home-hq", map[string]any{"type": "MEDIA_STATUS", "status": []any{map[string]any{"playerState": "PAUSED", "currentTime": 12, "media": map[string]any{"duration": 120, "metadata": map[string]string{"title": "Dinner soundtrack", "artist": "Artist"}}}}})
		done <- nil
	}()
	s, e := c.readCast(context.Background(), device("cast"), "")
	if e != nil || s.Title != "Dinner soundtrack" || s.Playback != "PAUSED" || s.Power != "" || len(s.CastPin) != 64 {
		t.Fatal(s, e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	go func() {
		conn, e := ln.Accept()
		if e == nil {
			conn.(*tls.Conn).Handshake()
			conn.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, e = c.deviceTLS(ctx, "192.168.1.20:8009", strings.Repeat("0", 64)); e == nil {
		t.Fatal("changed public key accepted")
	}
}

type fakeRepo struct {
	mu sync.Mutex
	d  Device
	c  Credential
}

func (r *fakeRepo) MediaDevices() ([]Device, error)  { return []Device{r.d}, nil }
func (r *fakeRepo) MergeMediaDevices([]Device) error { return nil }
func (r *fakeRepo) MediaCredential(string) (Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.c, nil
}
func (r *fakeRepo) SaveMediaCredential(_ string, c Credential) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.c = c
	return nil
}
func (r *fakeRepo) SaveMediaPin(_ string, p string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.c.CastPin = p
	return nil
}

type fakeProvider struct {
	calls   int
	failure bool
}

func (p *fakeProvider) Read(context.Context, Device, Credential) (Snapshot, error) {
	if p.failure {
		return Snapshot{}, errors.New("offline")
	}
	return Snapshot{Reachable: true, Power: "on", Title: "Movie", App: "Player", Playback: "PLAYING", Updated: time.Now()}, nil
}
func (p *fakeProvider) PairSamsung(context.Context, Device, Credential) (Credential, error) {
	return Credential{}, ErrAuth
}
func (p *fakeProvider) PairSony(context.Context, Device, Credential, string) (Credential, error) {
	return Credential{}, ErrAuth
}
func (p *fakeProvider) Command(context.Context, Device, Credential, string) error {
	p.calls++
	return nil
}
func TestServiceControlsAndStaleStatus(t *testing.T) {
	d := device("sony")
	r := &fakeRepo{d: d, c: Credential{Host: d.Host, SonyPSK: "secret"}}
	p := &fakeProvider{}
	s := NewService(r, p, nil)
	s.Refresh(context.Background())
	if e := s.Command(context.Background(), d.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	if s.Command(context.Background(), d.ID, "play") == nil || p.calls != 1 {
		t.Fatal("no rate limit")
	}
	if s.Command(context.Background(), d.ID, "power") == nil {
		t.Fatal("unknown command")
	}
	view := s.View(time.Now().Add(2 * time.Minute))
	if view.Cards[0].Controls() || view.Cards[0].Status.Playing() {
		t.Fatal("stale controls")
	}
	p.failure = true
	s.Refresh(context.Background())
	card := s.View(time.Now()).Cards[0]
	if card.Status.Title != "" || card.Status.App != "" || card.Controls() {
		t.Fatal("offline state retained playback", card)
	}
	if e := s.Forget(d.ID); e != nil {
		t.Fatal(e)
	}
	c, _ := r.MediaCredential(d.ID)
	if c.SonyPSK != "" {
		t.Fatal("forget retained key")
	}
}

func TestSamsungPairingArtStatusAndFixedControls(t *testing.T) {
	var mu sync.Mutex
	keys := []string{}
	sawArt := false
	server := httptest.NewTLSServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		path := ws.Request().URL.Path
		if strings.Contains(path, "samsung.remote.control") {
			websocket.JSON.Send(ws, map[string]any{"event": "ms.channel.connect", "data": map[string]string{"token": "test-token-123"}})
			var request struct {
				Method string
				Params struct{ DataOfCmd string }
			}
			if websocket.JSON.Receive(ws, &request) == nil {
				mu.Lock()
				keys = append(keys, request.Params.DataOfCmd)
				mu.Unlock()
			}
			return
		}
		var request struct {
			Method string
			Params struct{ Event, Data string }
		}
		if websocket.JSON.Receive(ws, &request) != nil {
			return
		}
		var payload struct{ Request, ID string }
		json.Unmarshal([]byte(request.Params.Data), &payload)
		mu.Lock()
		sawArt = payload.Request == "get_artmode_status" && len(payload.ID) == 36
		mu.Unlock()
		websocket.JSON.Send(ws, map[string]any{"event": "d2d_service_message", "data": `{"event":"artmode_status","value":"on"}`})
	}))
	defer server.Close()
	c := NewClient()
	c.Dial = func(ctx context.Context, n, a string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, n, server.Listener.Addr().String())
	}
	d := device("samsung")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	credential, e := c.PairSamsung(ctx, d, Credential{})
	if e != nil || credential.SamsungToken != "test-token-123" || len(credential.SamsungPin) != 64 {
		t.Fatal(credential, e)
	}
	known, art := c.readSamsungArt(ctx, d, credential)
	if !known || !art {
		t.Fatal("Art Mode reply not understood")
	}
	if e = c.Command(ctx, d, credential, "pause"); e != nil {
		t.Fatal(e)
	}
	// Wait for the in-process fixture to consume the final fixed command.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ready := len(keys) > 0
		mu.Unlock()
		if ready {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawArt || len(keys) != 1 || keys[0] != "KEY_PAUSE" {
		t.Fatal(sawArt, keys)
	}
}
