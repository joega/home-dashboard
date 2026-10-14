package dashboard

import (
	"context"
	"home-dashboard/internal/sports"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type dashboardSportsProvider struct{ calls int }

func (p *dashboardSportsProvider) Fetch(_ context.Context, t sports.Team, now time.Time) (sports.Snapshot, error) {
	p.calls++
	return sports.Snapshot{TeamKey: t.Key, Fetched: now, Games: []sports.Game{{ID: t.Key + "-one", TeamKey: t.Key, Start: now, Opponent: "<script>alert(1)</script>", State: "scheduled"}}}, nil
}
func (*dashboardSportsProvider) FetchLogo(context.Context, sports.Team) ([]byte, error) {
	return nil, http.ErrMissingFile
}
func TestSportsSettingsCachePersistenceAndReadOnlyPages(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.SportsSettings()
	if err != nil || len(cfg.Teams) != 5 || !cfg.Today {
		t.Fatal(cfg, err)
	}
	if s.SaveSportsSettings(sports.Settings{Teams: []string{"yankees"}}) == nil {
		t.Fatal("accepted a team outside household catalog")
	}
	p := &dashboardSportsProvider{}
	a.sports = sports.NewService(s, p, loc)
	a.sports.Refresh(context.Background(), time.Now().In(loc))
	before := p.calls
	for _, path := range []string{"/sports", "/sports?team=patriots&view=results", "/settings?section=sports", "/"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test"+path, nil))
		body := w.Body.String()
		if w.Code != 200 || strings.Contains(body, "<script>alert(1)</script>") {
			t.Fatal(path, w.Code, body)
		}
		if path == "/sports" && (!strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") || !strings.Contains(body, "New England Revolution") && !strings.Contains(body, "Revolution")) {
			t.Fatal("sports view missing or unescaped")
		}
	}
	if p.calls != before {
		t.Fatal("page views triggered provider fetches")
	}
	raw, err := s.LoadSportsCache("patriots")
	if err != nil || raw == nil || len(raw.Games) != 1 {
		t.Fatal(raw, err)
	}
	var peopleBefore, entriesBefore int
	s.db.QueryRow("SELECT count(*) FROM people").Scan(&peopleBefore)
	s.db.QueryRow("SELECT count(*) FROM entries").Scan(&entriesBefore)
	cfg = sports.Settings{Teams: []string{"bruins"}, Today: false}
	if err = s.SaveSportsSettings(cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := s.SportsSettings()
	if len(got.Teams) != 1 || got.Teams[0] != "bruins" || got.Today {
		t.Fatal(got)
	}
	var peopleAfter, entriesAfter int
	s.db.QueryRow("SELECT count(*) FROM people").Scan(&peopleAfter)
	s.db.QueryRow("SELECT count(*) FROM entries").Scan(&entriesAfter)
	if peopleBefore != peopleAfter || entriesBefore != entriesAfter {
		t.Fatal("sports settings modified household rows")
	}
	s.db.Exec("UPDATE sports_cache SET body='broken' WHERE team='patriots'")
	raw, err = s.LoadSportsCache("patriots")
	if err != nil || raw != nil {
		t.Fatal("invalid cache accepted", raw, err)
	}
}
func TestSportsSettingsCSRFAndLocalLogoFallback(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	body := url.Values{"teams": {"red-sox", "bruins"}, "today": {"on"}, "csrf": {strings.Repeat("a", 64)}}
	for _, valid := range []bool{false, true} {
		r := httptest.NewRequest("POST", "http://household.test/settings/sports", strings.NewReader(body.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		if valid {
			r.AddCookie(&http.Cookie{Name: "hq_csrf", Value: strings.Repeat("a", 64)})
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if valid && (w.Code != 200 || !strings.Contains(w.Body.String(), "Sports settings saved.")) || !valid && w.Code != 403 {
			t.Fatal(valid, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/sports/logos/red-sox", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/sports/logos/unreviewed", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
