package dashboard

import (
	"context"
	"home-dashboard/internal/spotify"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type perPersonSpotifyProvider struct {
	mu       sync.Mutex
	commands []string
}

func (p *perPersonSpotifyProvider) Refresh(_ context.Context, c spotify.Credential) (spotify.Credential, error) {
	c.Revision = spotify.Random()
	c.Expires = time.Now().Add(time.Hour)
	return c, nil
}
func (p *perPersonSpotifyProvider) Playback(_ context.Context, c spotify.Credential) (spotify.Snapshot, error) {
	n := 25
	return spotify.Snapshot{Title: "Song for " + c.AccountName, Device: spotify.Device{ID: "device-" + c.AccountID, Name: "Speaker for " + c.AccountName, SupportsVolume: true, Volume: &n}, Playing: true, Fetched: time.Now()}, nil
}
func (p *perPersonSpotifyProvider) Devices(_ context.Context, c spotify.Credential) ([]spotify.Device, error) {
	n := 25
	return []spotify.Device{{ID: "device-" + c.AccountID, Name: "Speaker for " + c.AccountName, SupportsVolume: true, Volume: &n}}, nil
}
func (p *perPersonSpotifyProvider) Command(_ context.Context, c spotify.Credential, d spotify.Device, action string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands = append(p.commands, c.AccountID+":"+d.ID+":"+action)
	return nil
}
func (p *perPersonSpotifyProvider) Artwork(context.Context, string) ([]byte, string, error) {
	return nil, "", http.ErrMissingFile
}
func (p *perPersonSpotifyProvider) Search(_ context.Context, _ spotify.Credential, q, kind string, offset int) (spotify.SearchResults, error) {
	return spotify.SearchResults{Query: q, Kind: kind, Searched: true, Items: []spotify.SearchItem{{ID: "0123456789abcdefghijkl", Kind: kind, Title: "Result"}}}, nil
}
func (p *perPersonSpotifyProvider) PlayItem(_ context.Context, c spotify.Credential, d spotify.Device, item spotify.SearchItem) error {
	return p.Command(context.Background(), c, d, "selection")
}
func (p *perPersonSpotifyProvider) SetVolume(_ context.Context, c spotify.Credential, d spotify.Device, level int) error {
	return p.Command(context.Background(), c, d, "volume-"+strconv.Itoa(level))
}
func setupSpotifyProfiles(t *testing.T) (*Store, *App, Person, Person, *perPersonSpotifyProvider) {
	t.Helper()
	s, _ := testStore(t)
	s.SavePerson(Person{Name: "Alex"})
	s.SavePerson(Person{Name: "Sam"})
	people, _ := s.People()
	alex, sam := people[0], people[1]
	cfg := spotify.Config{ClientID: strings.Repeat("a", 32), Redirect: "http://127.0.0.1:4182" + spotify.CallbackPath}
	s.SaveSpotifyConfig(cfg)
	cfg, _ = s.SpotifyConfig()
	for _, person := range []Person{alex, sam} {
		c := spotify.Credential{ClientID: cfg.ClientID, AccountID: "account-" + person.Name, AccountName: person.Name, AccessToken: "access-" + person.Name, RefreshToken: "refresh-" + person.Name, Revision: spotify.Random(), Scope: spotify.Scopes, Expires: time.Now().Add(time.Hour)}
		if e := s.SaveSpotifyConnectedFor(person.ID, cfg.Revision, "", c); e != nil {
			t.Fatal(e)
		}
	}
	a, e := New(s, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	p := &perPersonSpotifyProvider{}
	a.spotify = spotify.NewService(s, p)
	for _, person := range []Person{alex, sam} {
		a.spotifyService(person.ID).Refresh(context.Background())
	}
	return s, a, alex, sam, p
}
func TestSpotifyProfilesHaveIndependentCardsAndControls(t *testing.T) {
	s, a, alex, sam, provider := setupSpotifyProfiles(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://house.test/media", nil)
	r.AddCookie(&http.Cookie{Name: currentPersonCookie, Value: strconv.FormatInt(sam.ID, 10)})
	a.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Song for Alex") || !strings.Contains(body, "Song for Sam") || strings.Index(body, "Song for Sam") > strings.Index(body, "Song for Alex") {
		t.Fatal("independent cards or selected person's order", w.Code)
	}
	for _, private := range []string{"access-Alex", "refresh-Alex", "access-Sam", "refresh-Sam"} {
		if strings.Contains(body, private) {
			t.Fatal("credential leaked in cards")
		}
	}
	if e := a.spotifyService(alex.ID).Command(context.Background(), "device-account-Sam", "pause"); e == nil {
		t.Fatal("cross-account device accepted")
	}
	results, e := a.spotifyService(alex.ID).Search(context.Background(), "Song", "track", 0)
	if e != nil {
		t.Fatal(e)
	}
	if a.spotifyService(sam.ID).PlaySelection(context.Background(), "device-account-Sam", results.Items[0].Key) == nil {
		t.Fatal("cross-account search choice accepted")
	}
	if e := a.spotifyService(alex.ID).SetVolume(context.Background(), "device-account-Alex", 50); e != nil {
		t.Fatal(e)
	}
	if e := a.spotifyService(sam.ID).Command(context.Background(), "device-account-Sam", "pause"); e != nil {
		t.Fatal(e)
	}
	if len(provider.commands) != 2 || provider.commands[0] != "account-Alex:device-account-Alex:volume-50" || provider.commands[1] != "account-Sam:device-account-Sam:pause" {
		t.Fatal("wrong command routing", provider.commands)
	}
	if e := s.DisconnectSpotifyFor(alex.ID); e != nil {
		t.Fatal(e)
	}
	left, _ := s.SpotifyCredentialFor(alex.ID)
	right, _ := s.SpotifyCredentialFor(sam.ID)
	if left.Connected() || !right.Connected() {
		t.Fatal("disconnect affected another person")
	}
}
func TestSpotifyProfileCredentialAndLoginGuards(t *testing.T) {
	s, a, alex, sam, _ := setupSpotifyProfiles(t)
	cfg, _ := s.SpotifyConfig()
	alexCredential, _ := s.SpotifyCredentialFor(alex.ID)
	if s.SaveSpotifyConnectedFor(sam.ID, cfg.Revision, "", alexCredential) == nil {
		t.Fatal("same Spotify account linked twice")
	}
	link, _ := s.SpotifyLinkRevision(alex.ID)
	pending := spotify.Pending{PersonID: alex.ID, LinkRevision: link, StateHash: spotify.Hash("state-one"), BrowserHash: spotify.Hash("browser-one"), Verifier: spotify.Random(), Redirect: cfg.Redirect, ClientID: cfg.ClientID, ConfigRevision: cfg.Revision, Expires: time.Now().Add(time.Minute)}
	s.SaveSpotifyPending(pending)
	other := pending
	other.PersonID = sam.ID
	other.StateHash = spotify.Hash("state-two")
	other.BrowserHash = spotify.Hash("browser-two")
	s.SaveSpotifyPending(other)
	if _, e := s.ConsumeSpotifyPending("state-one", "browser-two", "127.0.0.1:4182"); e == nil {
		t.Fatal("browser binding lost")
	}
	consumed, e := s.ConsumeSpotifyPending("state-one", "browser-one", "127.0.0.1:4182")
	if e != nil || consumed.PersonID != alex.ID {
		t.Fatal("pending profile lost", e)
	}
	if _, e = s.ConsumeSpotifyPending("state-two", "browser-two", "127.0.0.1:4182"); e != nil {
		t.Fatal("other browser login erased", e)
	}
	s.DisconnectSpotifyFor(alex.ID)
	if s.SaveSpotifyConnectedFor(alex.ID, cfg.Revision, consumed.LinkRevision, alexCredential) == nil {
		t.Fatal("late callback resurrected disconnected account")
	}
	if s.SwapSpotifyCredentialFor(alex.ID, alexCredential.Revision, alexCredential) == nil {
		t.Fatal("late refresh resurrected account")
	}
	s.SetPersonActive(sam.ID, false)
	if a.spotifyService(sam.ID).View(time.Now()).Connected {
		t.Fatal("archived account remains active")
	}
	if s.SaveSpotifyConnectedFor(sam.ID, cfg.Revision, "", alexCredential) == nil {
		t.Fatal("archived target accepted")
	}
}
func TestSpotifyLegacyAssignmentKeepsConnectionAndScopesHTTP(t *testing.T) {
	s, a, alex, sam, _ := setupSpotifyProfiles(t)
	s.DisconnectSpotifyFor(alex.ID)
	cfg, _ := s.SpotifyConfig()
	legacy := spotify.Credential{ClientID: cfg.ClientID, AccountID: "legacy-account", AccountName: "Saved account", AccessToken: "legacy-access", RefreshToken: "legacy-refresh", Revision: spotify.Random(), Scope: spotify.Scopes, Expires: time.Now().Add(time.Hour)}
	if e := s.SaveSpotifyConnected(cfg.Revision, legacy); e != nil {
		t.Fatal(e)
	}
	if s.AssignLegacySpotify(sam.ID) == nil {
		t.Fatal("overwrote existing person's account")
	}
	if e := s.AssignLegacySpotify(alex.ID); e != nil {
		t.Fatal(e)
	}
	old, _ := s.SpotifyCredential()
	moved, _ := s.SpotifyCredentialFor(alex.ID)
	if old.Connected() || moved.RefreshToken != legacy.RefreshToken || moved.Revision == legacy.Revision {
		t.Fatal("legacy authorization not preserved")
	}
	a.spotifyService(alex.ID).Refresh(context.Background())
	csrf := strings.Repeat("c", 64)
	values := url.Values{"csrf": {csrf}, "spotify_person_id": {strconv.FormatInt(alex.ID, 10)}, "action": {"pause"}, "device_id": {"device-account-Sam"}}
	r := httptest.NewRequest("POST", "http://house.test/media/spotify/command", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r.AddCookie(&http.Cookie{Name: "hq_csrf", Value: csrf})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "no longer the active Spotify device") {
		t.Fatal("HTTP account routing failed", w.Body.String())
	}
	// Pending login's target is fixed even when the current-person cookie changes.
	values = url.Values{"csrf": {csrf}, "spotify_person_id": {strconv.FormatInt(alex.ID, 10)}}
	r = httptest.NewRequest("POST", "http://127.0.0.1:4182/media/spotify/connect", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "hq_csrf", Value: csrf})
	r.AddCookie(&http.Cookie{Name: currentPersonCookie, Value: strconv.FormatInt(sam.ID, 10)})
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal("connect route", w.Code)
	}
	address, _ := url.Parse(w.Header().Get("Location"))
	var browser string
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == spotifyCookie {
			browser = cookie.Value
		}
	}
	p, e := s.ConsumeSpotifyPending(address.Query().Get("state"), browser, "127.0.0.1:4182")
	if e != nil || p.PersonID != alex.ID {
		t.Fatal("login followed current person instead of target", e)
	}
}
