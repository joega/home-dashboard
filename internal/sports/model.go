package sports

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// This dashboard follows one household's five teams, not a whole-league ticker.
type Team struct{ Key, Name, FullName, League, ProviderID, Route, Logo, Abbr string }

var teams = []Team{
	{"red-sox", "Red Sox", "Boston Red Sox", "MLB", "111", "", "https://a.espncdn.com/i/teamlogos/mlb/500/bos.png", "BOS"},
	{"bruins", "Bruins", "Boston Bruins", "NHL", "6", "", "https://a.espncdn.com/i/teamlogos/nhl/500/bos.png", "BOS"},
	{"patriots", "Patriots", "New England Patriots", "NFL", "17", "football/nfl", "https://a.espncdn.com/i/teamlogos/nfl/500/ne.png", "NE"},
	{"celtics", "Celtics", "Boston Celtics", "NBA", "2", "basketball/nba", "https://a.espncdn.com/i/teamlogos/nba/500/bos.png", "BOS"},
	{"revolution", "Revolution", "New England Revolution", "MLS", "189", "soccer/usa.1", "https://a.espncdn.com/i/teamlogos/soccer/500/189.png", "NE"},
}

func Teams() []Team { return append([]Team(nil), teams...) }
func Find(key string) (Team, bool) {
	for _, t := range teams {
		if t.Key == key {
			return t, true
		}
	}
	return Team{}, false
}

type Settings struct {
	Teams []string
	Today bool
}

func Defaults() Settings {
	c := Settings{Today: true}
	for _, t := range teams {
		c.Teams = append(c.Teams, t.Key)
	}
	return c
}
func (c Settings) Has(key string) bool {
	for _, k := range c.Teams {
		if k == key {
			return true
		}
	}
	return false
}
func (c Settings) Validate() error {
	if len(c.Teams) > len(teams) {
		return errors.New("Choose only the five household teams.")
	}
	seen := map[string]bool{}
	for _, key := range c.Teams {
		if _, ok := Find(key); !ok || seen[key] {
			return errors.New("Invalid or repeated team.")
		}
		seen[key] = true
	}
	return nil
}

type Game struct {
	ID, TeamKey, Opponent, OpponentAbbr, State, Detail, Venue, Broadcast, Stage, ProviderDate, Outcome string
	Start                                                                                              time.Time
	Home, TBD, Retained                                                                                bool
	Score, OpponentScore                                                                               *int
}
type Snapshot struct {
	TeamKey string
	Fetched time.Time
	Games   []Game
}

func (s Snapshot) Validate(team Team) error {
	if s.TeamKey != team.Key || s.Fetched.IsZero() || len(s.Games) > 256 {
		return errors.New("Invalid sports snapshot")
	}
	seen := map[string]bool{}
	for _, g := range s.Games {
		if g.Start.Year() > s.Fetched.Year()+2 || g.Start.Year() < s.Fetched.Year()-3 || g.TeamKey != team.Key || g.ID == "" || len(g.ID) > 80 || g.Start.IsZero() || g.Opponent == "" || len(g.Opponent) > 120 || len(g.Detail) > 100 || len(g.Venue) > 160 || len(g.Broadcast) > 160 || len(g.Stage) > 60 || seen[g.ID] {
			return errors.New("Invalid sports event")
		}
		seen[g.ID] = true
		if g.Outcome != "" && g.Outcome != "W" && g.Outcome != "L" && g.Outcome != "D" {
			return errors.New("Invalid final outcome")
		}
		switch g.State {
		case "scheduled", "live", "final", "postponed", "canceled", "unknown":
		default:
			return errors.New("Invalid game state")
		}
		for _, score := range []*int{g.Score, g.OpponentScore} {
			if score != nil && (*score < 0 || *score > 999) {
				return errors.New("Invalid score")
			}
		}
		if g.ProviderDate != "" {
			if _, err := time.Parse("2006-01-02", g.ProviderDate); err != nil {
				return err
			}
		}
	}
	return nil
}
func sortGames(g []Game) {
	sort.SliceStable(g, func(i, j int) bool {
		if g[i].Start.Equal(g[j].Start) {
			return g[i].ID < g[j].ID
		}
		return g[i].Start.Before(g[j].Start)
	})
}
func nextRefresh(s Snapshot, now time.Time) time.Time {
	next := now.Add(6 * time.Hour)
	for _, g := range s.Games {
		if g.State == "live" {
			return now.Add(2 * time.Minute)
		}
		// Scheduled games in their likely playing window still revalidate if a
		// provider hasn't switched their state yet. Time-TBD games aren't fake clocks.
		if !g.TBD && g.State == "scheduled" && now.After(g.Start.Add(-10*time.Minute)) && now.Before(g.Start.Add(8*time.Hour)) {
			return now.Add(2 * time.Minute)
		}
		if g.State == "scheduled" && !g.TBD && g.Start.After(now) && g.Start.Add(-10*time.Minute).Before(next) {
			next = g.Start.Add(-10 * time.Minute)
		}
	}
	return next
}

type TeamState struct {
	Team                      Team
	Fetched                   time.Time
	Ready, Stale, Unavailable bool
	Games                     []Game
}
type Card struct {
	Team                                                                                        Team
	ID, Opponent, OpponentAbbr, Day, Date, Time, Status, Score, Result, Venue, Broadcast, Stage string
	Home, Live, Stale, Final                                                                    bool
	Start                                                                                       time.Time
}

func gameDate(g Game, loc *time.Location) string {
	if g.TBD && g.ProviderDate != "" {
		return g.ProviderDate
	}
	return g.Start.In(loc).Format("2006-01-02")
}
func MakeCard(g Game, t Team, now time.Time, stale bool) Card {
	local := g.Start.In(now.Location())
	day := gameDate(g, now.Location())
	d, _ := time.ParseInLocation("2006-01-02", day, now.Location())
	c := Card{Team: t, ID: g.ID, Opponent: g.Opponent, OpponentAbbr: g.OpponentAbbr, Day: day, Date: d.Format("Mon, Jan 2"), Time: local.Format("3:04 PM"), Start: g.Start, Final: g.State == "final", Home: g.Home, Venue: g.Venue, Broadcast: g.Broadcast, Stage: g.Stage, Stale: stale || g.Retained}
	if d.Year() != now.Year() {
		c.Date = d.Format("Mon, Jan 2, 2006")
	}
	if day == now.Format("2006-01-02") {
		c.Date = "Today"
	}
	if g.TBD {
		c.Time = "Time TBD"
	}
	switch g.State {
	case "final":
		c.Status = "Final"
		if g.Detail != "" && g.Detail != "Final" {
			c.Status = g.Detail
		}
	case "live":
		c.Status = "In progress"
		c.Live = true
		if g.Detail != "" {
			c.Status = g.Detail
		}
	case "postponed":
		c.Status = "Postponed"
	case "canceled":
		c.Status = "Canceled"
	case "unknown":
		c.Status = "Status unavailable"
	default:
		c.Status = "Scheduled"
	}
	if (g.State == "final" || g.State == "live") && g.Score != nil && g.OpponentScore != nil {
		c.Score = fmt.Sprintf("%d – %d", *g.Score, *g.OpponentScore)
		if g.State == "final" {
			if *g.Score > *g.OpponentScore {
				c.Result = "W"
			} else if *g.Score < *g.OpponentScore {
				c.Result = "L"
			} else {
				c.Result = "D"
			}
			if g.Outcome != "" {
				c.Result = g.Outcome
			}
		}
	}
	return c
}

type Group struct {
	Label string
	Cards []Card
}
type View struct {
	Filter, Mode, Empty string
	Groups              []Group
	States              []TeamState
	Today               []Card
	TodayCount          int
	Warnings            []string
	Updated             string
}

func MakeView(states []TeamState, cfg Settings, filter, mode string, now time.Time) View {
	if _, ok := Find(filter); !ok || !cfg.Has(filter) {
		filter = ""
	}
	if mode != "results" {
		mode = "upcoming"
	}
	v := View{Filter: filter, Mode: mode, States: states}
	var oldest time.Time
	var loading, unavailable bool
	for _, state := range states {
		if !cfg.Has(state.Team.Key) {
			continue
		}
		if !state.Ready {
			loading = true
		}
		if state.Unavailable {
			unavailable = true
			v.Warnings = append(v.Warnings, state.Team.Name+" schedule unavailable")
		}
		if state.Stale && !state.Unavailable {
			v.Warnings = append(v.Warnings, state.Team.Name+" · cached schedule")
		}
		if !state.Fetched.IsZero() && (oldest.IsZero() || state.Fetched.Before(oldest)) {
			oldest = state.Fetched
		}
	}
	if !oldest.IsZero() {
		v.Updated = oldest.In(now.Location()).Format("Jan 2, 3:04 PM")
	}

	var picked []Game
	stale := map[string]bool{}
	for _, state := range states {
		t := state.Team
		if !cfg.Has(t.Key) {
			continue
		}
		stale[t.Key] = state.Stale
		if !state.Unavailable {
			for _, g := range state.Games {
				if gameDate(g, now.Location()) == now.Format("2006-01-02") {
					v.Today = append(v.Today, MakeCard(g, t, now, state.Stale))
				}
			}
		}
		if filter != "" && filter != t.Key || state.Unavailable {
			continue
		}
		var eligible, inRange []Game
		cutoff := now.AddDate(0, 0, 30).Format("2006-01-02")
		beginning := now.AddDate(0, 0, -30).Format("2006-01-02")
		for _, g := range state.Games {
			day := gameDate(g, now.Location())
			if mode == "results" {
				if (g.State == "final" || g.State == "canceled") && day <= now.Format("2006-01-02") {
					eligible = append(eligible, g)
					if day >= beginning && day <= now.Format("2006-01-02") {
						inRange = append(inRange, g)
					}
				}
			} else if g.State != "final" && g.State != "canceled" && (day >= now.Format("2006-01-02") || g.State == "live" || g.State == "postponed" && day >= beginning) {
				eligible = append(eligible, g)
				if day <= cutoff {
					inRange = append(inRange, g)
				}
			}
		}
		sortGames(eligible)
		// Still show a team's next/most recent game across an off-season gap.
		if len(inRange) == 0 && len(eligible) > 0 {
			if mode == "results" {
				inRange = eligible[len(eligible)-1:]
			} else {
				inRange = eligible[:1]
			}
		}
		picked = append(picked, inRange...)
	}
	sortGames(picked)
	if mode == "results" {
		for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
			picked[i], picked[j] = picked[j], picked[i]
		}
	}
	if len(picked) > 80 {
		picked = picked[:80]
	}
	for _, g := range picked {
		t, _ := Find(g.TeamKey)
		c := MakeCard(g, t, now, stale[g.TeamKey])
		if len(v.Groups) == 0 || v.Groups[len(v.Groups)-1].Label != c.Date {
			v.Groups = append(v.Groups, Group{Label: c.Date})
		}
		n := len(v.Groups) - 1
		v.Groups[n].Cards = append(v.Groups[n].Cards, c)
	}
	sort.SliceStable(v.Today, func(i, j int) bool { return v.Today[i].Start.Before(v.Today[j].Start) })
	v.TodayCount = len(v.Today)
	if len(v.Today) > 2 {
		v.Today = v.Today[:2]
	}
	v.Empty = "No upcoming games are listed for these teams."
	if mode == "results" {
		v.Empty = "No recent results are listed for these teams."
	}
	if len(picked) == 0 && loading {
		v.Empty = "Loading Boston schedules…"
	} else if len(picked) == 0 && unavailable {
		v.Empty = "No games to show from the available schedules."
	}
	if len(cfg.Teams) == 0 {
		v.Empty = "Choose your teams in Settings → Sports."
	}
	return v
}
func clean(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}
