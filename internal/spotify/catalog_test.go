package spotify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testTrackID = "0123456789abcdefghijkl"

func TestCatalogRequestFormatsAndExactVolume(t *testing.T) {
	commands := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-access" {
			t.Error("missing bearer")
		}
		if r.URL.Path == "/search" {
			q := r.URL.Query()
			if q.Get("q") != "Artist & song" || q.Get("type") != "track" || q.Get("limit") != "10" || q.Get("offset") != "10" {
				t.Error(q)
			}
			io.WriteString(w, `{"tracks":{"total":30,"next":"http://evil.test/","items":[{"id":"`+testTrackID+`","uri":"spotify:track:`+testTrackID+`","name":"A song","artists":[{"name":"Artist"}],"album":{"name":"Album"},"duration_ms":120000},{"id":"`+testTrackID+`","uri":"spotify:track:`+testTrackID+`","name":"Unavailable","is_playable":false},{"id":"`+testTrackID+`","uri":"arbitrary:uri","name":"Invalid"}]}}`)
			return
		}
		if r.Method != "PUT" || r.URL.Query().Get("device_id") != "speaker" {
			t.Error("bad command", r.Method, r.URL.String())
		}
		if r.URL.Path == "/me/player/volume" {
			commands = append(commands, r.URL.Query().Get("volume_percent"))
		} else {
			var body map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&body)
			if v, ok := body["uris"]; ok {
				if string(v) != `["spotify:track:`+testTrackID+`"]` {
					t.Error(string(v))
				}
				commands = append(commands, "track")
			} else {
				if string(body["context_uri"]) != `"spotify:album:`+testTrackID+`"` || string(body["offset"]) != `{"position":0}` {
					t.Error(body)
				}
				commands = append(commands, "album")
			}
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	c := NewClient()
	c.API = server.URL
	cred := Credential{AccessToken: "test-access"}
	r, e := c.Search(context.Background(), cred, "Artist & song", "track", 10)
	if e != nil || len(r.Items) != 1 || !r.HasNext() {
		t.Fatal(r, e)
	}
	volume := 10
	d := Device{ID: "speaker", SupportsVolume: true, Volume: &volume}
	for _, level := range []int{50, 0, 100} {
		if e = c.SetVolume(context.Background(), cred, d, level); e != nil {
			t.Fatal(e)
		}
	}
	for _, level := range []int{-5, 101, 16} {
		if c.SetVolume(context.Background(), cred, d, level) == nil {
			t.Fatal("invalid volume accepted")
		}
	}
	for _, kind := range []string{"track", "album"} {
		if e = c.PlayItem(context.Background(), cred, d, SearchItem{ID: testTrackID, Kind: kind, Title: "Title"}); e != nil {
			t.Fatal(e)
		}
	}
	if strings.Join(commands, ",") != "50,0,100,track,album" {
		t.Fatal(commands)
	}
	for _, bad := range []struct {
		q, kind string
		offset  int
	}{{"", "track", 0}, {strings.Repeat("a", 161), "track", 0}, {"song", "artist", 0}, {"song", "track", 1}, {"song", "track", 1010}} {
		if _, e = c.Search(context.Background(), cred, bad.q, bad.kind, bad.offset); e == nil {
			t.Fatal("invalid search accepted")
		}
	}
}

type catalogProvider struct {
	*fakeProvider
	searches, plays, volume int
}

func (p *catalogProvider) Search(_ context.Context, _ Credential, q, kind string, offset int) (SearchResults, error) {
	p.searches++
	return SearchResults{Query: q, Kind: kind, Offset: offset, Searched: true, Total: 1, Items: []SearchItem{{ID: testTrackID, Kind: kind, Title: "Title"}}}, nil
}
func (p *catalogProvider) PlayItem(context.Context, Credential, Device, SearchItem) error {
	p.plays++
	return nil
}
func (p *catalogProvider) SetVolume(_ context.Context, _ Credential, _ Device, level int) error {
	p.volume = level
	return nil
}
func TestServiceSelectionAndVolumeGuards(t *testing.T) {
	ctx := context.Background()
	setup := func() (*fakeRepo, *catalogProvider, *Service) {
		r, p := fixture()
		r.c.AccountID = "test-account"
		volume := 10
		p.state.Device.SupportsVolume = true
		p.state.Device.Volume = &volume
		p.ds = []Device{p.state.Device}
		provider := &catalogProvider{fakeProvider: p}
		s := NewService(r, provider)
		s.Refresh(ctx)
		return r, provider, s
	}
	r, p, s := setup()
	results, e := s.Search(ctx, "Title", "album", 0)
	if e != nil || len(results.Items) != 1 || results.Items[0].Key == "" {
		t.Fatal(results, e)
	}
	key := results.Items[0].Key
	if s.PlaySelection(ctx, "echo", "tampered") == nil || s.PlaySelection(ctx, "unknown", key) == nil || p.plays != 0 {
		t.Fatal("tampered selection/device accepted")
	}
	if e = s.PlaySelection(ctx, "echo", key); e != nil || p.plays != 1 {
		t.Fatal(e, p.plays)
	}
	r, p, s = setup()
	results, _ = s.Search(ctx, "Title", "track", 0)
	key = results.Items[0].Key
	r.mu.Lock()
	r.c.AccountID = "different-account"
	r.mu.Unlock()
	if s.PlaySelection(ctx, "echo", key) == nil || p.plays != 0 {
		t.Fatal("cross-account selection")
	}
	_, p, s = setup()
	results, _ = s.Search(ctx, "Title", "track", 0)
	key = results.Items[0].Key
	s.mu.Lock()
	choice := s.selections[key]
	choice.expires = time.Now().Add(-time.Second)
	s.selections[key] = choice
	s.mu.Unlock()
	if s.PlaySelection(ctx, "echo", key) == nil || p.plays != 0 {
		t.Fatal("expired selection")
	}
	_, p, s = setup()
	if s.SetVolume(ctx, "echo", 16) == nil || s.SetVolume(ctx, "wrong", 50) == nil {
		t.Fatal("invalid volume accepted")
	}
	if e = s.SetVolume(ctx, "echo", 50); e != nil || p.volume != 50 {
		t.Fatal("exact volume failed", e)
	}
	_, p, s = setup()
	p.state.Disallows["setting_volume"] = true
	s.Refresh(ctx)
	if s.SetVolume(ctx, "echo", 50) == nil || p.volume != 0 {
		t.Fatal("restricted volume accepted")
	}
}
