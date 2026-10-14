package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPKCEAndRedirectGuards(t *testing.T) {
	if Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal("PKCE RFC vector")
	}
	cfg := Config{ClientID: strings.Repeat("a", 32), Redirect: "http://127.0.0.1:4182" + CallbackPath}
	if cfg.Validate() != nil {
		t.Fatal("loopback rejected")
	}
	for _, address := range []string{"http://192.168.50.10:4173" + CallbackPath, "http://localhost:4182" + CallbackPath, "http://user@127.0.0.1:4182" + CallbackPath, cfg.Redirect + "?x=1", "http://127.0.0.1:0" + CallbackPath} {
		cfg.Redirect = address
		if cfg.Validate() == nil {
			t.Fatal("bad redirect", address)
		}
	}
	if AllowedImage("https://i.scdn.co.evil.test/art") || AllowedImage("http://i.scdn.co/art") || AllowedImage("https://user@i.scdn.co/art") {
		t.Fatal("untrusted artwork accepted")
	}
}
func TestClientExchangeRefreshPlaybackAndCommands(t *testing.T) {
	var commands []string
	var tokenForms []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.String(), "secret") {
			t.Error("credential in URL")
		}
		if r.URL.Path == "/token" {
			r.ParseForm()
			tokenForms = append(tokenForms, r.Form)
			if r.Header.Get("Authorization") != "" {
				t.Error("bearer sent to token endpoint")
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				io.WriteString(w, `{"access_token":"new-secret","token_type":"Bearer","expires_in":3600}`)
			} else {
				io.WriteString(w, `{"access_token":"access-secret","refresh_token":"refresh-secret","token_type":"Bearer","expires_in":3600,"scope":"user-read-playback-state user-modify-playback-state"}`)
			}
			return
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("missing auth")
		}
		if r.Method != "GET" {
			commands = append(commands, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
			if r.URL.Path == "/me/player" {
				var body struct {
					Devices []string `json:"device_ids"`
					Play    bool
				}
				json.NewDecoder(r.Body).Decode(&body)
				if body.Play || len(body.Devices) != 1 {
					t.Error("transfer changed playback")
				}
			}
			w.WriteHeader(204)
			return
		}
		switch r.URL.Path {
		case "/me":
			io.WriteString(w, `{"id":"demo-user","display_name":"Alex"}`)
		case "/me/player/devices":
			io.WriteString(w, `{"devices":[{"id":"echo-1","name":"Kitchen speaker","type":"Speaker","is_restricted":false,"supports_volume":false,"volume_percent":20}]}`)
		case "/me/player":
			io.WriteString(w, `{"device":{"id":"echo-1","name":"Kitchen speaker","is_restricted":false,"supports_volume":false},"is_playing":true,"progress_ms":1000,"item":{"name":"A song","duration_ms":180000,"artists":[{"name":"Artist"}],"album":{"name":"Album","images":[{"url":"https://i.scdn.co/image/cover"}]},"external_urls":{"spotify":"https://open.spotify.com/track/song"}},"actions":{"disallows":{"skipping_next":true}}}`)
		}
	}))
	defer server.Close()
	client := NewClient()
	client.API = server.URL
	client.Token = server.URL + "/token"
	pending := Pending{Verifier: Random(), Redirect: "http://127.0.0.1:4182" + CallbackPath, ClientID: strings.Repeat("a", 32)}
	credential, e := client.Exchange(context.Background(), pending, "code")
	if e != nil || credential.AccountName != "Alex" || !credential.CanControl() {
		t.Fatal(credential, e)
	}
	if len(tokenForms) != 1 || tokenForms[0].Get("code_verifier") != pending.Verifier || tokenForms[0].Get("client_secret") != "" {
		t.Fatal("incorrect PKCE exchange")
	}
	updated, e := client.Refresh(context.Background(), credential)
	if e != nil || updated.RefreshToken != credential.RefreshToken || updated.Scope != credential.Scope || updated.Revision == credential.Revision {
		t.Fatal(updated, e)
	}
	s, e := client.Playback(context.Background(), updated)
	if e != nil || s.Title != "A song" || s.Device.Name != "Kitchen speaker" || !s.Playing || !s.Disallows["skipping_next"] {
		t.Fatal(s, e)
	}
	ds, e := client.Devices(context.Background(), updated)
	if e != nil || len(ds) != 1 || ds[0].SupportsVolume {
		t.Fatal(ds, e)
	}
	if client.Command(context.Background(), updated, ds[0], "volume-up") == nil || client.Command(context.Background(), updated, ds[0], "arbitrary") == nil {
		t.Fatal("unapproved command")
	}
	if len(commands) != 0 {
		t.Fatal("bad commands sent")
	}
	if e = client.Command(context.Background(), updated, ds[0], "pause"); e != nil {
		t.Fatal(e)
	}
	if e = client.Command(context.Background(), updated, ds[0], "transfer"); e != nil {
		t.Fatal(e)
	}
	if len(commands) != 2 || !strings.HasPrefix(commands[0], "PUT /me/player/pause?device_id=echo-1") {
		t.Fatal(commands)
	}
}
func TestNoPlayerRateLimitAndSanitizedErrors(t *testing.T) {
	code := 204
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code == 429 {
			w.Header().Set("Retry-After", "120")
		}
		w.WriteHeader(code)
		if code != 204 {
			io.WriteString(w, `{"message":"leaked-access-token"}`)
		}
	}))
	defer server.Close()
	c := NewClient()
	c.API = server.URL
	cred := Credential{AccessToken: "secret"}
	s, e := c.Playback(context.Background(), cred)
	if e != nil || s.Title != "" || s.Device.ID != "" {
		t.Fatal(s, e)
	}
	for _, n := range []int{401, 403, 429, 500} {
		code = n
		_, e = c.Playback(context.Background(), cred)
		if e == nil || strings.Contains(e.Error(), "leaked") {
			t.Fatal(n, e)
		}
		if n == 429 {
			var rate RateError
			if !errors.As(e, &rate) || rate.Delay != 120*time.Second {
				t.Fatal(e)
			}
		}
	}
}

type fakeRepo struct {
	mu  sync.Mutex
	cfg Config
	c   Credential
}

func (r *fakeRepo) SpotifyConfig() (Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg, nil
}
func (r *fakeRepo) SpotifyCredential() (Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.c, nil
}
func (r *fakeRepo) SwapSpotifyCredential(old string, c Credential) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old != r.c.Revision {
		return ErrChanged
	}
	r.c = c
	return nil
}

type fakeProvider struct {
	state            Snapshot
	ds               []Device
	commands         int
	started, release chan struct{}
}

func (p *fakeProvider) Refresh(_ context.Context, c Credential) (Credential, error) {
	c.Expires = time.Now().Add(time.Hour)
	c.Revision = Random()
	return c, nil
}
func (p *fakeProvider) Playback(context.Context, Credential) (Snapshot, error) {
	if p.started != nil {
		close(p.started)
		<-p.release
	}
	s := p.state
	s.Fetched = time.Now()
	return s, nil
}
func (p *fakeProvider) Devices(context.Context, Credential) ([]Device, error) { return p.ds, nil }
func (p *fakeProvider) Command(context.Context, Credential, Device, string) error {
	p.commands++
	return nil
}
func (*fakeProvider) Artwork(context.Context, string) ([]byte, string, error) {
	return nil, "", errors.New("no image")
}
func fixture() (*fakeRepo, *fakeProvider) {
	d := Device{ID: "echo", Name: "Kitchen speaker", Active: true}
	r := &fakeRepo{cfg: Config{ClientID: strings.Repeat("a", 32), Redirect: "http://127.0.0.1:4182" + CallbackPath}, c: Credential{AccessToken: "a", RefreshToken: "r", ClientID: strings.Repeat("a", 32), Scope: Scopes, Revision: "revision", Expires: time.Now().Add(time.Hour)}}
	return r, &fakeProvider{state: Snapshot{Device: d, Title: "Song", Playing: true, Disallows: map[string]bool{}}, ds: []Device{d}}
}
func TestServiceDeviceRestrictionStalenessAndLateDisconnect(t *testing.T) {
	r, p := fixture()
	s := NewService(r, p)
	s.Refresh(context.Background())
	v := s.View(time.Now())
	if !v.Controls() || v.VolumeControls() {
		t.Fatal(v)
	}
	if s.Command(context.Background(), "tampered", "pause") == nil || s.Command(context.Background(), "echo", "volume-up") == nil || s.Command(context.Background(), "echo", "unknown") == nil || p.commands != 0 {
		t.Fatal("unsafe control accepted")
	}
	if e := s.Command(context.Background(), "echo", "pause"); e != nil {
		t.Fatal(e)
	}
	if s.Command(context.Background(), "echo", "play") == nil {
		t.Fatal("immediate command accepted")
	}
	if s.View(time.Now().Add(time.Minute)).Controls() {
		t.Fatal("stale controls")
	}
	r, p = fixture()
	p.started = make(chan struct{})
	p.release = make(chan struct{})
	s = NewService(r, p)
	done := make(chan struct{})
	go func() { s.Refresh(context.Background()); close(done) }()
	<-p.started
	r.mu.Lock()
	r.c = Credential{}
	r.mu.Unlock()
	s.Reset()
	close(p.release)
	<-done
	v = s.View(time.Now())
	if v.Connected || v.State.Title != "" {
		t.Fatal("late fetch resurrected session", v)
	}
}
