package sports

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixedClient(t *testing.T, response func(*http.Request) string) *Client {
	t.Helper()
	return &Client{HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response(r)))}, nil
	})}}
}

const espnFinal = `{"id":"1","date":"2026-10-12T23:00Z","timeValid":true,"seasonType":{"name":"Preseason"},"competitions":[{"timeValid":true,"venue":{"fullName":"Arena"},"status":{"type":{"state":"post","completed":true,"name":"STATUS_FINAL","shortDetail":"Final"}},"competitors":[{"homeAway":"home","team":{"id":"2","displayName":"Boston Celtics","abbreviation":"BOS"},"score":{"value":100}},{"homeAway":"away","team":{"id":"5","displayName":"Cleveland Cavaliers","abbreviation":"CLE"},"score":{"value":99}}]}]}`
const espnFuture = `{"id":"2","date":"2027-01-10T05:00Z","timeValid":false,"seasonType":{"name":"Regular Season"},"competitions":[{"status":{"type":{"state":"pre","name":"STATUS_SCHEDULED","shortDetail":"TBD"}},"competitors":[{"homeAway":"away","team":{"id":"2","displayName":"Boston Celtics"},"score":{"value":0}},{"homeAway":"home","team":{"id":"5","displayName":"Cleveland Cavaliers"},"score":{"value":0}}]}]}`

func TestESPNSeasonUnionDoesNotInventScoresOrTimes(t *testing.T) {
	team, _ := Find("celtics")
	now := time.Date(2026, 10, 13, 16, 0, 0, 0, time.UTC)
	calls := 0
	client := fixedClient(t, func(r *http.Request) string {
		calls++
		if r.URL.Host != "site.api.espn.com" {
			t.Fatal(r.URL)
		}
		events := espnFinal
		if r.URL.Query().Get("seasontype") == "2" {
			events = espnFuture
		}
		if r.URL.Query().Get("seasontype") == "3" {
			events = ""
		}
		return `{"team":{"id":"2"},"season":{"year":2027,"type":1},"events":[` + events + `]}`
	})
	got, err := client.Fetch(context.Background(), team, now)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(got.Games) != 2 {
		t.Fatal(calls, got)
	}
	final, future := got.Games[0], got.Games[1]
	if final.Score == nil || *final.Score != 100 || final.OpponentScore == nil || *final.OpponentScore != 99 {
		t.Fatal(final)
	}
	if future.Score != nil || future.OpponentScore != nil || !future.TBD || future.Stage != "" {
		t.Fatal(future)
	}
	loc, _ := time.LoadLocation("America/New_York")
	card := MakeCard(future, team, now.In(loc), false)
	if card.Time != "Time TBD" || card.Day != "2027-01-10" || !strings.Contains(card.Date, "2027") {
		t.Fatal(card)
	}
}
func TestRevolutionRequestsResultsAndFixtures(t *testing.T) {
	team, _ := Find("revolution")
	calls := 0
	client := fixedClient(t, func(r *http.Request) string {
		calls++
		id, date, state, score := "1", "2026-10-10T23:30Z", "post", `,"score":{"value":0}`
		if r.URL.Query().Get("fixture") == "true" {
			id, date, state, score = "2", "2026-10-14T23:30Z", "pre", ""
		}
		return `{"team":{"id":"189"},"season":{"year":2026},"events":[{"id":"` + id + `","date":"` + date + `","competitions":[{"status":{"type":{"state":"` + state + `"}},"competitors":[{"homeAway":"home","team":{"id":"189","displayName":"New England Revolution"}` + score + `},{"homeAway":"away","team":{"id":"9","displayName":"Opponent"}` + score + `}]}]}]}`
	})
	got, err := client.Fetch(context.Background(), team, time.Now())
	if err != nil || calls != 2 || len(got.Games) != 2 {
		t.Fatal(got, err, calls)
	}
	if got.Games[0].Score == nil || *got.Games[0].Score != 0 || got.Games[1].Score != nil {
		t.Fatal(got)
	}
}
func TestNHLStateAndHomeAwayOrientation(t *testing.T) {
	team, _ := Find("bruins")
	client := fixedClient(t, func(*http.Request) string {
		return `{"games":[{"id":1,"startTimeUTC":"2026-10-12T23:00:00Z","gameDate":"2026-10-12","gameState":"OFF","gameScheduleState":"OK","homeTeam":{"id":3,"placeName":{"default":"New York"},"commonName":{"default":"Rangers"},"score":3},"awayTeam":{"id":6,"score":2},"periodDescriptor":{"periodType":"OT"}},{"id":2,"startTimeUTC":"2026-10-14T23:00:00Z","gameDate":"2026-10-14","gameState":"FUT","gameScheduleState":"PPD","homeTeam":{"id":6,"score":0},"awayTeam":{"id":3,"placeName":{"default":"New York"},"commonName":{"default":"Rangers"},"score":0}}]}`
	})
	got, err := client.Fetch(context.Background(), team, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Games[0].Home || *got.Games[0].Score != 2 || got.Games[0].Detail != "Final / OT" || got.Games[1].State != "postponed" || got.Games[1].Score != nil {
		t.Fatal(got)
	}
}
func TestMLBRejectsIncompleteSlateAndHandlesDoubleHeaders(t *testing.T) {
	team, _ := Find("red-sox")
	incomplete := false
	client := fixedClient(t, func(r *http.Request) string {
		if r.URL.Query().Get("teamId") != "111" {
			t.Fatal(r.URL)
		}
		body := `{"totalGames":2,"dates":[{"games":[{"gamePk":1,"gameDate":"2026-10-12T17:00:00Z","officialDate":"2026-10-12","status":{"abstractGameState":"Final"},"teams":{"home":{"team":{"id":111,"name":"Boston Red Sox"},"score":3},"away":{"team":{"id":147,"name":"New York Yankees"},"score":1}},"gameType":"R"},{"gamePk":2,"gameDate":"2026-10-12T23:00:00Z","officialDate":"2026-10-12","status":{"abstractGameState":"Preview","detailedState":"Postponed"},"teams":{"home":{"team":{"id":111,"name":"Boston Red Sox"},"score":0},"away":{"team":{"id":147,"name":"New York Yankees"},"score":0}},"gameType":"R"}]}]}`
		if incomplete {
			body = strings.Replace(body, `"totalGames":2`, `"totalGames":3`, 1)
		}
		return body
	})
	got, err := client.Fetch(context.Background(), team, time.Now())
	if err != nil || len(got.Games) != 2 || got.Games[0].ID == got.Games[1].ID || got.Games[1].Score != nil || got.Games[1].State != "postponed" {
		t.Fatal(got, err)
	}
	incomplete = true
	if _, err = client.Fetch(context.Background(), team, time.Now()); err == nil {
		t.Fatal("accepted incomplete slate")
	}
}
func TestProviderBoundsWrongTeamsAndMissingIDs(t *testing.T) {
	team, _ := Find("celtics")
	for _, body := range []string{strings.Repeat("x", maxBody+1), `{"team":{"id":"5"},"events":[]}`, `{"team":{"id":"2"},"season":{"year":2027},"events":null}`, `{"team":{"id":"2"},"season":{"year":2027,"type":3},"events":[` + strings.Replace(espnFinal, `"id":"1"`, `"id":""`, 1) + `]}`} {
		c := fixedClient(t, func(*http.Request) string { return body })
		if _, err := c.Fetch(context.Background(), team, time.Now()); err == nil {
			t.Fatal("accepted invalid response")
		}
	}
	team.Logo = "http://127.0.0.1/private"
	c := fixedClient(t, func(*http.Request) string { t.Fatal("unknown URL was fetched"); return "" })
	if _, err := c.FetchLogo(context.Background(), team); err == nil {
		t.Fatal("allowed arbitrary logo URL")
	}
}
func number(n int) *int { return &n }
func TestViewUsesHouseholdDatesAndOffSeasonFallback(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 10, 13, 20, 0, 0, 0, loc)
	sox, _ := Find("red-sox")
	pats, _ := Find("patriots")
	states := []TeamState{{Team: sox, Ready: true, Fetched: now, Games: []Game{{ID: "last", TeamKey: sox.Key, Start: now.AddDate(0, -2, 0), Opponent: "Yankees", State: "final", Score: nil, OpponentScore: number(1)}, {ID: "next", TeamKey: sox.Key, Start: now.AddDate(0, 5, 0), Opponent: "Orioles", State: "scheduled"}}}, {Team: pats, Ready: true, Fetched: now, Stale: true, Games: []Game{{ID: "tonight", TeamKey: pats.Key, Start: time.Date(2026, 10, 14, 0, 30, 0, 0, time.UTC), Opponent: "Bills", State: "scheduled"}}}}
	v := MakeView(states, Defaults(), "", "upcoming", now)
	if len(v.Groups) != 2 || v.Groups[0].Label != "Today" || v.TodayCount != 1 || v.Groups[0].Cards[0].Time != "8:30 PM" || len(v.Warnings) != 1 {
		t.Fatal(v)
	}
	v = MakeView(states, Defaults(), sox.Key, "results", now)
	if len(v.Groups) != 1 || v.Groups[0].Cards[0].Score != "" || v.Groups[0].Cards[0].Result != "" {
		t.Fatal("missing score inferred as a loss", v)
	}
	v = MakeView(states, Settings{}, "", "upcoming", now)
	if len(v.Groups) != 0 || v.TodayCount != 0 || !strings.Contains(v.Empty, "Settings") {
		t.Fatal(v)
	}
}

type memoryRepo struct {
	settings Settings
	cache    map[string]Snapshot
	saved    int
}

func (r *memoryRepo) SportsSettings() (Settings, error) { return r.settings, nil }
func (r *memoryRepo) LoadSportsCache(k string) (*Snapshot, error) {
	v, ok := r.cache[k]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (r *memoryRepo) SaveSportsCache(v Snapshot) error      { r.cache[v.TeamKey] = v; r.saved++; return nil }
func (r *memoryRepo) LoadSportsLogo(string) ([]byte, error) { return []byte("cached logo"), nil }
func (r *memoryRepo) SaveSportsLogo(string, []byte) error   { return nil }

type fakeProvider struct {
	mu    sync.Mutex
	calls int
	fail  bool
}

func (p *fakeProvider) Fetch(ctx context.Context, t Team, now time.Time) (Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.fail {
		return Snapshot{}, errors.New("offline")
	}
	return Snapshot{TeamKey: t.Key, Fetched: now, Games: []Game{}}, nil
}
func (p *fakeProvider) FetchLogo(context.Context, Team) ([]byte, error) {
	return nil, errors.New("unused")
}
func TestServiceCacheRestartSingleFlightFailureBackoffAndDisable(t *testing.T) {
	now := time.Date(2026, 10, 13, 17, 0, 0, 0, time.UTC)
	team, _ := Find("patriots")
	cached := Snapshot{TeamKey: team.Key, Fetched: now, Games: []Game{{ID: "last", TeamKey: team.Key, Start: now.Add(-time.Hour), State: "final", Opponent: "Bills", Score: number(21), OpponentScore: number(10)}}}
	repo := &memoryRepo{settings: Settings{Teams: []string{team.Key}}, cache: map[string]Snapshot{team.Key: cached}}
	p := &fakeProvider{}
	svc := NewService(repo, p, time.UTC)
	svc.Refresh(context.Background(), now.Add(time.Hour))
	if p.calls != 0 {
		t.Fatal("restart bypassed fresh cache")
	}
	for i := 0; i < 10; i++ {
		svc.States(repo.settings, now)
		svc.Wake()
	}
	if p.calls != 0 {
		t.Fatal("page read made provider requests")
	}
	p.fail = true
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); svc.Refresh(context.Background(), now.Add(7*time.Hour)) }()
	}
	wg.Wait()
	if p.calls != 1 || repo.saved != 0 {
		t.Fatal("failed refresh duplicated or replaced cache", p.calls, repo.saved)
	}
	state := svc.States(repo.settings, now.Add(7*time.Hour))[0]
	if !state.Stale || state.Unavailable || len(state.Games) != 1 {
		t.Fatal(state)
	}
	svc.Refresh(context.Background(), now.Add(7*time.Hour+30*time.Second))
	if p.calls != 1 {
		t.Fatal("failure backoff bypassed")
	}
	if !svc.States(repo.settings, now.Add(8*24*time.Hour))[0].Unavailable {
		t.Fatal("expired cache was presented as usable")
	}
	repo.settings.Teams = nil
	svc.Refresh(context.Background(), now.Add(9*24*time.Hour))
	if p.calls != 1 {
		t.Fatal("disabled team was fetched")
	}
}
func TestNormalizedCacheIsBoundedAndRejectsDuplicateEvents(t *testing.T) {
	t1, _ := Find("patriots")
	s := Snapshot{TeamKey: t1.Key, Fetched: time.Now(), Games: []Game{{ID: "one", TeamKey: t1.Key, Start: time.Now(), Opponent: "Bills", State: "scheduled"}}}
	s.Games = append(s.Games, s.Games[0])
	if s.Validate(t1) == nil {
		t.Fatal("accepted repeated event")
	}
	body, _ := json.Marshal(s)
	if len(body) == 0 {
		t.Fatal("fixture unavailable")
	}
}

func TestSportsRedirectsStayOnTheHTTPSProviderHost(t *testing.T) {
	c := NewClient()
	start, _ := http.NewRequest("GET", "https://api-web.nhle.com/v1/club-schedule-season/BOS/now", nil)
	for _, test := range []struct {
		url     string
		allowed bool
	}{{"https://api-web.nhle.com/v1/club-schedule-season/BOS/20262027", true}, {"http://api-web.nhle.com/", false}, {"https://127.0.0.1/private", false}, {"https://api-web.nhle.com:8443/", false}, {"https://user:password@api-web.nhle.com/", false}} {
		request, _ := http.NewRequest("GET", test.url, nil)
		err := c.HTTP.CheckRedirect(request, []*http.Request{start})
		if (err == nil) != test.allowed {
			t.Fatal(test.url, err)
		}
	}
}

func TestServiceRetainsLastFinalAcrossRollingWindow(t *testing.T) {
	now := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	team, _ := Find("red-sox")
	previous := Snapshot{TeamKey: team.Key, Fetched: now.Add(-24 * time.Hour), Games: []Game{{ID: "last-final", TeamKey: team.Key, Start: now.AddDate(0, -2, 0), State: "final", Opponent: "Yankees", Score: number(4), OpponentScore: number(2)}}}
	repo := &memoryRepo{settings: Settings{Teams: []string{team.Key}}, cache: map[string]Snapshot{team.Key: previous}}
	svc := NewService(repo, &fakeProvider{}, time.UTC)
	svc.Refresh(context.Background(), now)
	saved := repo.cache[team.Key]
	if len(saved.Games) != 1 || !saved.Games[0].Retained {
		t.Fatal(saved)
	}
	view := MakeView(svc.States(repo.settings, now), repo.settings, "", "results", now)
	if len(view.Groups) != 1 || !view.Groups[0].Cards[0].Stale || view.Groups[0].Cards[0].Result != "W" {
		t.Fatal(view)
	}
}

func TestReportedShootoutWinnerOverridesTiedDisplayedScore(t *testing.T) {
	team, _ := Find("revolution")
	game := Game{ID: "pens", TeamKey: team.Key, Start: time.Now(), Opponent: "Opponent", State: "final", Score: number(2), OpponentScore: number(2), Outcome: "W", Detail: "FT-Pens"}
	card := MakeCard(game, team, time.Now(), false)
	if card.Result != "W" || card.Score != "2 – 2" {
		t.Fatal(card)
	}
}
