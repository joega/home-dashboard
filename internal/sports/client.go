package sports

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxBody = 2 << 20

// Sources/payload states adapt Sportray's provider adapters. URLs are fixed by
// the reviewed five-team catalog; no arbitrary provider/image URL is accepted.
type Client struct{ HTTP *http.Client }

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) == 0 || len(via) > 2 || r.URL.Scheme != "https" || r.URL.User != nil || r.URL.Host != via[0].URL.Host {
			return errors.New("Sports redirect leaves the reviewed provider host")
		}
		return nil
	}}}
}
func (c *Client) get(ctx context.Context, address string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Home-HQ/1.0")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Sports provider HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(body) > maxBody {
		return errors.New("Sports response too large")
	}
	return json.Unmarshal(body, target)
}
func (c *Client) Fetch(ctx context.Context, team Team, now time.Time) (Snapshot, error) {
	known, ok := Find(team.Key)
	if !ok || known != team {
		return Snapshot{}, errors.New("Unknown household team")
	}
	var games []Game
	var err error
	switch team.Key {
	case "bruins":
		games, err = c.nhl(ctx, team)
	case "red-sox":
		games, err = c.mlb(ctx, team, now)
	default:
		games, err = c.espn(ctx, team)
	}
	if err != nil {
		return Snapshot{}, err
	}
	sortGames(games)
	s := Snapshot{TeamKey: team.Key, Fetched: now, Games: games}
	return s, s.Validate(team)
}
func (c *Client) FetchLogo(ctx context.Context, t Team) ([]byte, error) {
	known, ok := Find(t.Key)
	if !ok || known != t {
		return nil, errors.New("Unknown team logo")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", t.Logo, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("Logo unavailable")
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 1<<20 {
		return nil, errors.New("Oversized logo")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || format != "png" || cfg.Width > 1024 || cfg.Height > 1024 || cfg.Width < 1 || cfg.Height < 1 {
		return nil, errors.New("Invalid PNG logo")
	}
	return b, nil
}

// ESPN team schedules expose score objects (unlike scoreboard string scores).
type espnSchedule struct {
	Team struct {
		ID string `json:"id"`
	} `json:"team"`
	Season struct{ Year, Type int } `json:"season"`
	Events []struct {
		ID, Date     string
		TimeValid    *bool
		SeasonType   struct{ Name string }
		Competitions []struct {
			Date      string
			TimeValid *bool
			Venue     struct{ FullName string }
			Status    struct {
				Type struct {
					Name, State, ShortDetail string
					Completed                bool
				}
			}
			Broadcasts  []struct{ Media struct{ ShortName string } }
			Competitors []struct {
				HomeAway string
				Winner   *bool
				Team     struct{ ID, DisplayName, Abbreviation string }
				Score    *struct {
					Value        *float64
					DisplayValue string
					Winner       *bool
				}
			}
		}
	} `json:"events"`
}

func (c *Client) espn(ctx context.Context, t Team) ([]Game, error) {
	base := "https://site.api.espn.com/apis/site/v2/sports/" + t.Route + "/teams/" + t.ProviderID + "/schedule"
	var first espnSchedule
	if err := c.get(ctx, base, &first); err != nil {
		return nil, err
	}
	if first.Team.ID != t.ProviderID || first.Events == nil || first.Season.Year < 2000 || first.Season.Year > 2100 {
		return nil, errors.New("Unexpected team schedule")
	}
	responses := []espnSchedule{first}
	addresses := []string{}
	if t.Key == "revolution" {
		addresses = append(addresses, base+"?fixture=true")
	} else {
		if first.Season.Type != 2 {
			addresses = append(addresses, base+"?season="+strconv.Itoa(first.Season.Year)+"&seasontype=2")
		}
		if first.Season.Type != 3 {
			addresses = append(addresses, base+"?season="+strconv.Itoa(first.Season.Year)+"&seasontype=3")
		}
	}
	for _, address := range addresses {
		var d espnSchedule
		if err := c.get(ctx, address, &d); err != nil {
			return nil, err
		}
		if d.Team.ID != t.ProviderID || d.Events == nil {
			return nil, errors.New("Unexpected seasonal schedule")
		}
		responses = append(responses, d)
	}
	combined := map[string]Game{}
	for _, d := range responses {
		if len(d.Events) > 256 {
			return nil, errors.New("Too many schedule events")
		}
		seen := map[string]bool{}
		for _, e := range d.Events {
			if e.ID == "" || seen[e.ID] {
				return nil, errors.New("Repeated or missing ESPN game id")
			}
			seen[e.ID] = true
			if len(e.Competitions) != 1 {
				return nil, errors.New("Unexpected game competition")
			}
			comp := e.Competitions[0]
			if len(comp.Competitors) != 2 {
				return nil, errors.New("Unexpected game competitors")
			}
			own, other := 0, 1
			if comp.Competitors[own].Team.ID != t.ProviderID {
				own, other = 1, 0
			}
			ours, opponent := comp.Competitors[own], comp.Competitors[other]
			if ours.Team.ID != t.ProviderID || opponent.Team.ID == "" || opponent.Team.ID == t.ProviderID || (ours.HomeAway != "home" && ours.HomeAway != "away") {
				return nil, errors.New("Schedule contains wrong team")
			}
			start, err := parseStart(e.Date)
			if err != nil {
				return nil, err
			}
			g := Game{ID: "espn:" + e.ID, TeamKey: t.Key, Start: start, ProviderDate: e.Date[:10], Home: ours.HomeAway == "home", Opponent: clean(opponent.Team.DisplayName, 120), OpponentAbbr: clean(opponent.Team.Abbreviation, 12), State: espnState(comp.Status.Type.Name, comp.Status.Type.State, comp.Status.Type.Completed), Detail: clean(comp.Status.Type.ShortDetail, 100), Venue: clean(comp.Venue.FullName, 160), Stage: clean(e.SeasonType.Name, 60), TBD: e.TimeValid != nil && !*e.TimeValid || comp.TimeValid != nil && !*comp.TimeValid}
			if strings.EqualFold(g.Stage, "Regular Season") {
				g.Stage = ""
			}
			if g.State == "scheduled" {
				g.Detail = ""
				if strings.Contains(strings.ToUpper(comp.Status.Type.ShortDetail), "TBD") {
					g.TBD = true
				}
			}
			if g.State == "live" || g.State == "final" {
				for i, side := range []int{own, other} {
					score := comp.Competitors[side].Score
					var n *int
					if score != nil {
						if score.Value != nil {
							v := *score.Value
							if v >= 0 && v <= 999 && v == float64(int(v)) {
								iv := int(v)
								n = &iv
							}
						} else if v, err := strconv.Atoi(score.DisplayValue); err == nil && v >= 0 && v <= 999 {
							n = &v
						}
					}
					if i == 0 {
						g.Score = n
					} else {
						g.OpponentScore = n
					}
				}
			}
			if g.State == "final" {
				won, lost := ours.Winner, opponent.Winner
				if won == nil && ours.Score != nil {
					won = ours.Score.Winner
				}
				if lost == nil && opponent.Score != nil {
					lost = opponent.Score.Winner
				}
				if won != nil && *won {
					g.Outcome = "W"
				} else if lost != nil && *lost {
					g.Outcome = "L"
				}
			}
			var broadcasts []string
			for _, b := range comp.Broadcasts {
				if name := clean(b.Media.ShortName, 40); name != "" {
					broadcasts = append(broadcasts, name)
				}
			}
			g.Broadcast = clean(strings.Join(broadcasts, " · "), 160)
			combined[g.ID] = g
		}
	}
	games := []Game{}
	for _, g := range combined {
		games = append(games, g)
	}
	return games, nil
}
func espnState(name, state string, completed bool) string {
	name = strings.ToLower(name)
	if strings.Contains(name, "postpon") || strings.Contains(name, "delay") || strings.Contains(name, "suspend") {
		return "postponed"
	}
	if strings.Contains(name, "cancel") {
		return "canceled"
	}
	if completed || state == "post" {
		return "final"
	}
	if state == "in" {
		return "live"
	}
	if state == "pre" {
		return "scheduled"
	}
	return "unknown"
}
func parseStart(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00"} {
		if t, e := time.Parse(layout, value); e == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("Invalid game start")
}

type nhlSchedule struct {
	Games []struct {
		ID                                                   int64
		StartTimeUTC, GameDate, GameState, GameScheduleState string
		HomeTeam, AwayTeam                                   struct {
			ID                    int
			Abbrev                string
			PlaceName, CommonName struct{ Default string }
			Score                 *int
		}
		Venue            struct{ Default string }
		TVBroadcasts     []struct{ Network string }
		GameType         int
		PeriodDescriptor struct{ PeriodType string }
	}
}

func (c *Client) nhl(ctx context.Context, t Team) ([]Game, error) {
	var d nhlSchedule
	if err := c.get(ctx, "https://api-web.nhle.com/v1/club-schedule-season/BOS/now", &d); err != nil {
		return nil, err
	}
	if d.Games == nil || len(d.Games) > 256 {
		return nil, errors.New("Unexpected NHL schedule")
	}
	games := []Game{}
	for _, e := range d.Games {
		if e.ID <= 0 {
			return nil, errors.New("Missing NHL game id")
		}
		ours, other := e.HomeTeam, e.AwayTeam
		home := true
		if ours.ID != 6 {
			ours, other = e.AwayTeam, e.HomeTeam
			home = false
		}
		if ours.ID != 6 || other.ID == 6 || other.ID <= 0 {
			return nil, errors.New("Unexpected NHL team")
		}
		start, err := parseStart(e.StartTimeUTC)
		if err != nil {
			return nil, err
		}
		g := Game{ID: "nhl:" + strconv.FormatInt(e.ID, 10), TeamKey: t.Key, Start: start, ProviderDate: e.GameDate, Home: home, Opponent: clean(other.PlaceName.Default+" "+other.CommonName.Default, 120), OpponentAbbr: clean(other.Abbrev, 12), Venue: clean(e.Venue.Default, 160), TBD: e.GameScheduleState == "TBD"}
		switch e.GameState {
		case "FINAL", "OFF":
			g.State = "final"
			g.Detail = "Final"
			if e.PeriodDescriptor.PeriodType == "OT" || e.PeriodDescriptor.PeriodType == "SO" {
				g.Detail = "Final / " + e.PeriodDescriptor.PeriodType
			}
		case "LIVE", "CRIT":
			g.State = "live"
		case "FUT", "PRE":
			g.State = "scheduled"
		case "PPD", "SUSP":
			g.State = "postponed"
		case "CNCL":
			g.State = "canceled"
		default:
			g.State = "unknown"
		}
		if e.GameScheduleState == "PPD" || e.GameScheduleState == "SUSP" {
			g.State = "postponed"
		}
		if g.State == "final" || g.State == "live" {
			g.Score = ours.Score
			g.OpponentScore = other.Score
		}
		if e.GameType == 1 {
			g.Stage = "Preseason"
		}
		if e.GameType == 3 {
			g.Stage = "Playoffs"
		}
		var b []string
		for _, v := range e.TVBroadcasts {
			if name := clean(v.Network, 40); name != "" {
				b = append(b, name)
			}
		}
		g.Broadcast = clean(strings.Join(b, " · "), 160)
		games = append(games, g)
	}
	return games, nil
}

type mlbSchedule struct {
	TotalGames *int
	Dates      []struct {
		Games []struct {
			GamePK                 int64
			GameDate, OfficialDate string
			Status                 struct {
				AbstractGameState, DetailedState string
				StartTimeTBD                     bool
			}
			Teams struct {
				Home, Away struct {
					Team struct {
						ID                 int
						Name, Abbreviation string
					}
					Score *int
				}
			}
			Venue    struct{ Name string }
			GameType string
		}
	}
}

func (c *Client) mlb(ctx context.Context, t Team, now time.Time) ([]Game, error) {
	q := url.Values{"sportId": {"1"}, "teamId": {"111"}, "startDate": {now.AddDate(0, 0, -30).Format("2006-01-02")}, "endDate": {now.AddDate(0, 0, 365).Format("2006-01-02")}, "hydrate": {"team,linescore"}}
	var d mlbSchedule
	if err := c.get(ctx, "https://statsapi.mlb.com/api/v1/schedule?"+q.Encode(), &d); err != nil {
		return nil, err
	}
	if d.TotalGames == nil || *d.TotalGames > 256 || d.Dates == nil {
		return nil, errors.New("Unexpected MLB schedule")
	}
	games := []Game{}
	for _, day := range d.Dates {
		for _, e := range day.Games {
			if e.GamePK <= 0 {
				return nil, errors.New("Missing MLB game id")
			}
			ours, other := e.Teams.Home, e.Teams.Away
			home := true
			if ours.Team.ID != 111 {
				ours, other = e.Teams.Away, e.Teams.Home
				home = false
			}
			if ours.Team.ID != 111 || other.Team.ID == 111 || other.Team.ID <= 0 {
				return nil, errors.New("Unexpected MLB team")
			}
			start, err := parseStart(e.GameDate)
			if err != nil {
				return nil, err
			}
			g := Game{ID: "mlb:" + strconv.FormatInt(e.GamePK, 10), TeamKey: t.Key, Start: start, ProviderDate: e.OfficialDate, Home: home, Opponent: clean(other.Team.Name, 120), OpponentAbbr: clean(other.Team.Abbreviation, 12), Venue: clean(e.Venue.Name, 160), TBD: e.Status.StartTimeTBD}
			detail := strings.ToLower(e.Status.DetailedState)
			switch {
			case strings.Contains(detail, "postpon") || strings.Contains(detail, "suspend") || strings.Contains(detail, "delay"):
				g.State = "postponed"
			case strings.Contains(detail, "cancel"):
				g.State = "canceled"
			case e.Status.AbstractGameState == "Final" || detail == "game over":
				g.State = "final"
			case e.Status.AbstractGameState == "Live":
				g.State = "live"
			case e.Status.AbstractGameState == "Preview":
				g.State = "scheduled"
			default:
				g.State = "unknown"
			}
			if g.State == "final" || g.State == "live" {
				g.Score = ours.Score
				g.OpponentScore = other.Score
			}
			if e.GameType == "E" {
				g.Stage = "Exhibition"
			}
			if e.GameType == "S" {
				g.Stage = "Spring training"
			}
			if e.GameType == "F" || e.GameType == "D" || e.GameType == "L" || e.GameType == "W" {
				g.Stage = "Postseason"
			}
			games = append(games, g)
		}
	}
	if len(games) != *d.TotalGames {
		return nil, errors.New("Incomplete baseball schedule")
	}
	sort.SliceStable(games, func(i, j int) bool { return games[i].Start.Before(games[j].Start) })
	return games, nil
}
