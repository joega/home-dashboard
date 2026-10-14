package dashboard

import (
	"context"
	"encoding/json"
	"home-dashboard/internal/spotify"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type spotifyTransport func(*http.Request) (*http.Response, error)

func (f spotifyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type dashboardSpotifyProvider struct{ calls, commands int }

func (*dashboardSpotifyProvider) Refresh(_ context.Context, c spotify.Credential) (spotify.Credential, error) {
	return c, nil
}
func (p *dashboardSpotifyProvider) Playback(context.Context, spotify.Credential) (spotify.Snapshot, error) {
	p.calls++
	return spotify.Snapshot{Title: "<script>song</script>", Device: spotify.Device{ID: "echo", Name: "Joes Echo"}, Playing: true, Fetched: time.Now()}, nil
}
func (*dashboardSpotifyProvider) Devices(context.Context, spotify.Credential) ([]spotify.Device, error) {
	return []spotify.Device{{ID: "echo", Name: "Joes Echo"}}, nil
}
func (p *dashboardSpotifyProvider) Command(context.Context, spotify.Credential, spotify.Device, string) error {
	p.commands++
	return nil
}
func (*dashboardSpotifyProvider) Artwork(context.Context, string) ([]byte, string, error) {
	return nil, "", http.ErrMissingFile
}
func TestSpotifyOAuthStateCookieOneUseAndPrivateTokens(t *testing.T) {
	s, _ := testStore(t)
	cfg := spotify.Config{ClientID: strings.Repeat("a", 32), Redirect: "http://127.0.0.1:4182" + spotify.CallbackPath}
	if e := s.SaveSpotifyConfig(cfg); e != nil {
		t.Fatal(e)
	}
	a, e := New(s, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	csrf := strings.Repeat("c", 64)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "http://127.0.0.1:4182/media/spotify/connect", strings.NewReader(url.Values{"csrf": {csrf}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "hq_csrf", Value: csrf})
	a.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	auth, _ := url.Parse(w.Header().Get("Location"))
	state := auth.Query().Get("state")
	if state == "" || auth.Query().Get("code_challenge_method") != "S256" || auth.Query().Get("client_secret") != "" {
		t.Fatal("invalid authorize parameters")
	}
	var login *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == spotifyCookie {
			login = c
		}
	}
	if login == nil || !login.HttpOnly || login.SameSite != http.SameSiteLaxMode {
		t.Fatal("browser binding cookie missing")
	}
	requests := 0
	a.spotifyClient.HTTP.Transport = spotifyTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		body := `{"id":"joe","display_name":"Joe"}`
		if r.URL.Path == "/api/token" {
			r.ParseForm()
			if spotify.Challenge(r.Form.Get("code_verifier")) != auth.Query().Get("code_challenge") {
				t.Error("wrong PKCE verifier")
			}
			body = `{"access_token":"private-access-secret","refresh_token":"private-refresh-secret","token_type":"Bearer","expires_in":3600,"scope":"user-read-playback-state user-modify-playback-state"}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	callback := "http://127.0.0.1:4182" + spotify.CallbackPath + "?state=" + url.QueryEscape(state) + "&code=grant-code"
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", callback, nil)
	r.AddCookie(&http.Cookie{Name: spotifyCookie, Value: "wrong-browser"})
	a.ServeHTTP(w, r)
	if requests != 0 || !strings.Contains(w.Body.String(), "another browser") {
		t.Fatal("wrong browser accepted")
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", callback, nil)
	r.AddCookie(login)
	a.ServeHTTP(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "/media" || requests != 2 {
		t.Fatal(w.Code, w.Body.String(), requests)
	}
	credential, _ := s.SpotifyCredential()
	if credential.AccountName != "Joe" || credential.RefreshToken != "private-refresh-secret" {
		t.Fatal("OAuth connection not persisted")
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", callback, nil)
	r.AddCookie(login)
	a.ServeHTTP(w, r)
	if requests != 2 {
		t.Fatal("callback replayed")
	}
	provider := &dashboardSpotifyProvider{}
	a.spotify = spotify.NewService(s, provider)
	a.spotify.Refresh(context.Background())
	before := provider.calls
	for _, path := range []string{"/media", "/settings?section=media"} {
		w = httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:4182"+path, nil))
		body := w.Body.String()
		if w.Code != 200 || strings.Contains(body, credential.RefreshToken) || strings.Contains(body, credential.AccessToken) || strings.Contains(body, "<script>song</script>") {
			t.Fatal("private or unescaped data in HTML", w.Code)
		}
		if path == "/media" && !strings.Contains(body, "&lt;script&gt;song&lt;/script&gt;") {
			t.Fatal("missing escaped track")
		}
	}
	if provider.calls != before {
		t.Fatal("GET queried Spotify")
	}
	r = httptest.NewRequest("POST", "http://127.0.0.1:4182/media/spotify/command", strings.NewReader("action=pause&device_id=echo"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 || provider.commands != 0 {
		t.Fatal("command bypassed CSRF")
	}
	old := credential.Revision
	if e = s.DisconnectSpotify(); e != nil {
		t.Fatal(e)
	}
	credential.Revision = spotify.Random()
	if s.SwapSpotifyCredential(old, credential) == nil {
		t.Fatal("refresh resurrected disconnected credential")
	}
	if s.SaveSpotifyConnected("wrong-revision", credential) == nil {
		t.Fatal("stale config accepted")
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM spotify_private").Scan(&count)
	if count != 0 {
		t.Fatal("disconnect retained OAuth secrets")
	}
}
func TestSpotifyConfigChangesAndPendingExpiry(t *testing.T) {
	s, _ := testStore(t)
	cfg := spotify.Config{ClientID: strings.Repeat("a", 32), Redirect: "http://127.0.0.1:4182" + spotify.CallbackPath}
	s.SaveSpotifyConfig(cfg)
	cfg, _ = s.SpotifyConfig()
	c := spotify.Credential{ClientID: cfg.ClientID, AccessToken: "access", RefreshToken: "refresh", Revision: "revision", Expires: time.Now().Add(time.Hour)}
	s.SaveSpotifyConnected(cfg.Revision, c)
	p := spotify.Pending{StateHash: spotify.Hash("state"), BrowserHash: spotify.Hash("cookie"), Redirect: cfg.Redirect, ClientID: cfg.ClientID, ConfigRevision: cfg.Revision, Verifier: spotify.Random(), Expires: time.Now().Add(-time.Second)}
	s.SaveSpotifyPending(p)
	if _, e := s.ConsumeSpotifyPending("state", "cookie", "127.0.0.1:4182"); e == nil {
		t.Fatal("expired OAuth pending accepted")
	}
	cfg.ClientID = strings.Repeat("b", 32)
	s.SaveSpotifyConfig(cfg)
	c, _ = s.SpotifyCredential()
	if c.Connected() {
		t.Fatal("client change retained tokens")
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM spotify_private").Scan(&n)
	if n != 0 {
		t.Fatal("client change retained pending state")
	}
	raw, _ := json.Marshal(cfg)
	if strings.Contains(string(raw), "access") {
		t.Fatal("tokens in public config")
	}
}
