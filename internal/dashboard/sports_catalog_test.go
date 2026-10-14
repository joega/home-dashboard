package dashboard

import (
	"context"
	"home-dashboard/internal/sports"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExpandedTeamsSettingsPersistOrderAndOverflowDoesNotHideGames(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	// Existing household choices survive the catalog upgrade. Extra teams do not
	// start fetching until explicitly followed.
	before, _ := s.SportsSettings()
	if len(before.Teams) != 5 || before.Teams[0] != "red-sox" {
		t.Fatal(before)
	}
	keys := append(append([]string{}, before.Teams...), "epl-364", "nba-13", "mlb-147")
	cfg := sports.Settings{Teams: keys, Today: true}
	if err = s.SaveSportsSettings(cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := s.SportsSettings()
	if strings.Join(got.Teams, ",") != strings.Join(keys, ",") {
		t.Fatal(got)
	}
	provider := &dashboardSportsProvider{}
	a.sports = sports.NewService(s, provider, loc)
	a.sports.Refresh(context.Background(), time.Now().In(loc))
	if provider.calls != len(keys) {
		t.Fatal("fetched unfollowed catalog", provider.calls)
	}
	for _, path := range []string{"/sports", "/sports?team=nba-13&view=results", "/settings?section=sports"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test"+path, nil))
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatal(w.Code, body)
		}
		if strings.Contains(path, "section=sports") {
			if !strings.Contains(body, `data-team-search`) || !strings.Contains(body, `Follow Liverpool`) || !strings.Contains(body, `Follow Seattle Mariners`) {
				t.Fatal("catalog search missing")
			}
			continue
		}
		if !strings.Contains(body, `name="team"`) || !strings.Contains(body, `value="nba-13"`) {
			t.Fatal("missing overflow picker")
		}
		start := strings.Index(body, `class="sports-team-filters`)
		end := strings.Index(body[start:], `</nav>`) + start
		shortcuts := body[start:end]
		if strings.Count(shortcuts, `/sports/logos/`) != 6 || strings.Contains(shortcuts, `team=nba-13`) {
			t.Fatal("shortcut cap", shortcuts)
		}
		if strings.Contains(path, "nba-13") && !strings.Contains(body, `value="nba-13" selected`) {
			t.Fatal("overflow lost selected team/mode")
		}
		if path == "/sports" && !strings.Contains(body, "Yankees") {
			t.Fatal("All teams omitted overflow games")
		}
	}
	// URL query input cannot extend the request catalog or change selections.
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/sports?team="+url.QueryEscape("../../private"), nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), `team=../../private`) {
		t.Fatal(w.Code)
	}
}
