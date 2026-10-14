package sports

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCatalogIncludesAllSixLeaguesAndPreservesLegacyIdentities(t *testing.T) {
	counts := map[string]int{}
	for _, team := range Teams() {
		counts[team.League]++
	}
	for league, minimum := range map[string]int{"MLB": 30, "NHL": 32, "NFL": 32, "NBA": 30, "MLS": 30, "EPL": 20} {
		if counts[league] < minimum {
			t.Fatalf("%s catalog incomplete: %d", league, counts[league])
		}
	}
	for key, id := range map[string]string{"red-sox": "111", "bruins": "6", "patriots": "17", "celtics": "2", "revolution": "189"} {
		team, ok := Find(key)
		if !ok || team.ProviderID != id {
			t.Fatal("legacy identity changed", team)
		}
	}
	original := Teams()
	original[0].Key = "changed"
	if _, ok := Find("changed"); ok {
		t.Fatal("caller mutated catalog")
	}
	cfg := Settings{Teams: []string{"epl-364", "bruins", "mlb-147"}}
	selected := cfg.Selected()
	if cfg.Validate() != nil || len(selected) != 3 || selected[0].Key != "epl-364" || selected[2].Key != "mlb-147" {
		t.Fatal(selected)
	}
	for _, keys := range [][]string{{"unknown"}, {"bruins", "bruins"}} {
		if (Settings{Teams: keys}).Validate() == nil {
			t.Fatal("accepted invalid selection")
		}
	}
	keys := []string{}
	for _, team := range Teams()[:MaxFollowedTeams+1] {
		keys = append(keys, team.Key)
	}
	if (Settings{Teams: keys}).Validate() == nil {
		t.Fatal("unbounded selection")
	}
}

func TestOtherMLBAndNHLTeamsUseTheirOwnNativeIdentifiers(t *testing.T) {
	for _, league := range []string{"MLB", "NHL"} {
		t.Run(league, func(t *testing.T) {
			key := "mlb-147"
			if league == "NHL" {
				key = "nhl-28"
			}
			team, ok := Find(key)
			if !ok {
				t.Fatal(key)
			}
			client := fixedClient(t, func(r *http.Request) string {
				if league == "MLB" {
					if r.URL.Host != "statsapi.mlb.com" || r.URL.Query().Get("teamId") != "147" {
						t.Fatal(r.URL)
					}
					return `{"totalGames":1,"dates":[{"games":[{"gamePk":1,"gameDate":"2026-10-14T23:00Z","officialDate":"2026-10-14","status":{"abstractGameState":"Final"},"teams":{"home":{"team":{"id":111,"name":"Boston Red Sox"},"score":2},"away":{"team":{"id":147,"name":"New York Yankees"},"score":4}}}]}]}`
				}
				if r.URL.Host != "api-web.nhle.com" || r.URL.Path != "/v1/club-schedule-season/SJS/now" {
					t.Fatal(r.URL)
				}
				return `{"games":[{"id":1,"startTimeUTC":"2026-10-14T23:00Z","gameDate":"2026-10-14","gameState":"OFF","homeTeam":{"id":6,"placeName":{"default":"Boston"},"commonName":{"default":"Bruins"},"score":2},"awayTeam":{"id":28,"score":4}}]}`
			})
			snapshot, err := client.Fetch(context.Background(), team, time.Now())
			if err != nil || len(snapshot.Games) != 1 {
				t.Fatal(snapshot, err)
			}
			game := snapshot.Games[0]
			if game.Home || game.TeamKey != key || *game.Score != 4 || *game.OpponentScore != 2 {
				t.Fatal(game)
			}
		})
	}
}

func TestEPLAndOtherMLSTeamsCombineResultsAndFixtures(t *testing.T) {
	for _, key := range []string{"epl-364", "mls-18418"} {
		t.Run(key, func(t *testing.T) {
			team, ok := Find(key)
			if !ok {
				t.Fatal(key)
			}
			calls := 0
			c := fixedClient(t, func(r *http.Request) string {
				calls++
				if !strings.Contains(r.URL.Path, "/"+team.Route+"/teams/"+team.ProviderID+"/schedule") || r.URL.Query().Has("seasontype") {
					t.Fatal(r.URL)
				}
				state, id := "post", "finished"
				if r.URL.Query().Get("fixture") == "true" {
					state, id = "pre", "future"
				}
				return fmt.Sprintf(`{"team":{"id":%q},"season":{"year":2026},"events":[{"id":%q,"date":"2026-10-14T23:00Z","competitions":[{"status":{"type":{"state":%q}},"competitors":[{"homeAway":"home","team":{"id":%q,"displayName":%q},"score":{"value":2}},{"homeAway":"away","team":{"id":"999","displayName":"Opponent"},"score":{"value":1}}]}]}]}`, team.ProviderID, id, state, team.ProviderID, team.FullName)
			})
			got, err := c.Fetch(context.Background(), team, time.Now())
			if err != nil || calls != 2 || len(got.Games) != 2 {
				t.Fatal(got, err, calls)
			}
			for _, game := range got.Games {
				if game.State == "scheduled" && game.Score != nil {
					t.Fatal("invented future score", game)
				}
			}
		})
	}
}

func TestOtherNBAAndNFLTeamsUseTheirOwnRoutes(t *testing.T) {
	for _, key := range []string{"nba-13", "nfl-6"} {
		t.Run(key, func(t *testing.T) {
			team, ok := Find(key)
			if !ok {
				t.Fatal(key)
			}
			calls := 0
			c := fixedClient(t, func(r *http.Request) string {
				calls++
				if !strings.Contains(r.URL.Path, "/"+team.Route+"/teams/"+team.ProviderID+"/schedule") {
					t.Fatal(r.URL)
				}
				event := strings.ReplaceAll(espnFinal, `"id":"2"`, `"id":"`+team.ProviderID+`"`)
				if r.URL.Query().Get("seasontype") == "3" {
					event = ""
				}
				return fmt.Sprintf(`{"team":{"id":%q},"season":{"year":2026,"type":2},"events":[%s]}`, team.ProviderID, event)
			})
			got, err := c.Fetch(context.Background(), team, time.Now())
			if err != nil || calls != 2 || len(got.Games) != 1 {
				t.Fatal(got, err, calls)
			}
		})
	}
}
