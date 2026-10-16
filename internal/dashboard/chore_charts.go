package dashboard

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ChoreChartPerson struct {
	Person                    Person
	Key, URL, Badge           string
	Count, BestRun, NextBadge int
	Selected                  bool
}
type ChoreChartBucket struct {
	Label, Name      string
	Count, Height    int
	Future, Selected bool
}
type ChoreChart struct {
	Period, Date, Person, Label, Scope                      string
	PrevURL, NextURL, CurrentURL, DayURL, MonthURL, YearURL string
	TrendTitle, TrendCaption, PeriodName                    string
	Count, Helpers                                          int
	People                                                  []ChoreChartPerson
	Choices                                                 []Person
	Buckets                                                 []ChoreChartBucket
}
type choreDayCount struct {
	PersonID int64
	Day      string
	Count    int
}

// Read the retained credit ledger, never the append-only activity audit or
// today's mutable assignments. A reopened occurrence no longer has a credit row.
func (s *Store) choreDayCounts(start, end string) ([]choreDayCount, error) {
	rows, err := s.db.Query(`SELECT COALESCE(person_id,0),day,COUNT(*) FROM chore_history WHERE day>=? AND day<? GROUP BY person_id,day ORDER BY day,person_id`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []choreDayCount
	for rows.Next() {
		var d choreDayCount
		if err := rows.Scan(&d.PersonID, &d.Day, &d.Count); err != nil {
			return nil, err
		}
		if _, err := time.Parse(time.DateOnly, d.Day); err != nil {
			continue
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
func choreChartURL(period, date, person string) string {
	q := url.Values{"period": {period}, "date": {date}}
	if person != "" {
		q.Set("chart_person", person)
	}
	return "/chores?" + q.Encode()
}
func choreBadge(count int) (name string, next int) {
	switch {
	case count >= 100:
		return "Chore champion", 0
	case count >= 50:
		return "Household hero", 100
	case count >= 25:
		return "Team star", 50
	case count >= 10:
		return "Super helper", 25
	case count >= 5:
		return "Helping hand", 10
	case count >= 1:
		return "First star", 5
	default:
		return "Your first star awaits", 1
	}
}
func bestChoreRun(days []time.Time) int {
	best, run := 0, 0
	var previous time.Time
	for _, day := range days {
		if !previous.IsZero() && day.Format(time.DateOnly) == previous.AddDate(0, 0, 1).Format(time.DateOnly) {
			run++
		} else {
			run = 1
		}
		best = max(best, run)
		previous = day
	}
	return best
}
func (a *App) loadChoreCharts(p *Page, q url.Values) error {
	c := ChoreChart{Period: q.Get("period"), Person: q.Get("person"), Scope: "Everyone"}
	if c.Period != "day" && c.Period != "year" {
		c.Period = "month"
	}
	today, _ := time.ParseInLocation(time.DateOnly, p.Today, a.loc)
	date, err := time.ParseInLocation(time.DateOnly, q.Get("date"), a.loc)
	if err != nil || date.Year() < 1970 || date.After(today) {
		date = today
	}
	c.Date = date.Format(time.DateOnly)
	c.Choices = chorePeople(p.People)
	validPerson := c.Person == "" || c.Person == "household"
	if c.Person == "household" {
		c.Scope = "Household (unclaimed)"
	}
	for _, person := range c.Choices {
		if c.Person == strconv.FormatInt(person.ID, 10) {
			validPerson = true
			c.Scope = person.Name
		}
	}
	if !validPerson {
		c.Person = ""
		c.Scope = "Everyone"
	}
	start := date
	var end, previous, next time.Time
	switch c.Period {
	case "day":
		end = start.AddDate(0, 0, 1)
		previous = start.AddDate(0, 0, -1)
		next = end
		c.Label = date.Format("Mon, Jan 2, 2006")
		c.PeriodName = "on " + date.Format("Jan 2, 2006")
		c.TrendTitle = "Last 7 days"
		c.TrendCaption = "Through " + date.Format("Jan 2")
	case "year":
		start = time.Date(date.Year(), 1, 1, 0, 0, 0, 0, a.loc)
		end = start.AddDate(1, 0, 0)
		previous = start.AddDate(-1, 0, 0)
		next = end
		c.Label = start.Format("2006")
		c.PeriodName = "in " + start.Format("2006")
		c.TrendTitle = "Month by month"
		c.TrendCaption = "Recorded completions"
	default:
		start = time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, a.loc)
		end = start.AddDate(0, 1, 0)
		previous = start.AddDate(0, -1, 0)
		next = end
		c.Label = start.Format("January 2006")
		c.PeriodName = "in " + start.Format("Jan 2006")
		c.TrendTitle = "Day by day"
		c.TrendCaption = "Recorded completions"
	}
	if previous.Year() >= 1970 {
		c.PrevURL = choreChartURL(c.Period, previous.Format(time.DateOnly), c.Person)
	}
	if !next.After(today) {
		c.NextURL = choreChartURL(c.Period, next.Format(time.DateOnly), c.Person)
	}
	c.CurrentURL = choreChartURL(c.Period, p.Today, c.Person)
	c.DayURL = choreChartURL("day", c.Date, c.Person)
	c.MonthURL = choreChartURL("month", c.Date, c.Person)
	c.YearURL = choreChartURL("year", c.Date, c.Person)
	p.Return = choreChartURL(c.Period, c.Date, c.Person)
	scanStart := start
	if c.Period == "day" {
		scanStart = start.AddDate(0, 0, -6)
	}
	scanEnd := end
	if tomorrow := today.AddDate(0, 0, 1); scanEnd.After(tomorrow) {
		scanEnd = tomorrow
	}
	counts, err := a.store.choreDayCounts(scanStart.Format(time.DateOnly), scanEnd.Format(time.DateOnly))
	if err != nil {
		return err
	}
	totals := map[int64]int{}
	days := map[int64][]time.Time{}
	buckets := map[string]int{}
	eligible := map[int64]bool{0: true}
	for _, person := range c.Choices {
		eligible[person.ID] = true
	}
	for _, d := range counts {
		if !eligible[d.PersonID] {
			continue
		}
		day, _ := time.ParseInLocation(time.DateOnly, d.Day, a.loc)
		key := strconv.FormatInt(d.PersonID, 10)
		if d.PersonID == 0 {
			key = "household"
		}
		matches := c.Person == "" || c.Person == key
		if !day.Before(start) && day.Before(end) {
			totals[d.PersonID] += d.Count
			days[d.PersonID] = append(days[d.PersonID], day)
			if matches {
				c.Count += d.Count
			}
		}
		if matches {
			bucket := d.Day
			if c.Period == "year" {
				bucket = day.Format("2006-01")
			}
			buckets[bucket] += d.Count
		}
	}
	for _, person := range c.Choices {
		key := strconv.FormatInt(person.ID, 10)
		if c.Person != "" && c.Person != key {
			continue
		}
		if !person.Active && totals[person.ID] == 0 && c.Person != key {
			continue
		}
		row := ChoreChartPerson{Person: person, Key: key, Count: totals[person.ID], BestRun: bestChoreRun(days[person.ID]), Selected: c.Person == key}
		row.Badge, row.NextBadge = choreBadge(row.Count)
		row.URL = choreChartURL(c.Period, c.Date, key)
		c.People = append(c.People, row)
		if row.Count > 0 {
			c.Helpers++
		}
	}
	sort.SliceStable(c.People, func(i, j int) bool {
		return strings.ToLower(c.People[i].Person.Name) < strings.ToLower(c.People[j].Person.Name)
	})
	if (c.Person == "" && totals[0] > 0) || c.Person == "household" {
		row := ChoreChartPerson{Person: Person{Name: "Household"}, Key: "household", Count: totals[0], BestRun: bestChoreRun(days[0]), Badge: "Shared stars", Selected: c.Person == "household", URL: choreChartURL(c.Period, c.Date, "household")}
		c.People = append(c.People, row)
	}
	bucketEnd := end
	if c.Period == "day" {
		bucketEnd = start.AddDate(0, 0, 1)
	}
	maxCount := 1
	for day := scanStart; day.Before(bucketEnd); {
		key, label, name := day.Format(time.DateOnly), day.Format("2"), day.Format("Mon, Jan 2")
		if c.Period == "year" {
			key = day.Format("2006-01")
			label = day.Format("Jan")
			name = day.Format("January 2006")
		}
		if c.Period == "day" {
			label = day.Format("Mon")
			name = day.Format("Mon, Jan 2, 2006")
		}
		b := ChoreChartBucket{Label: label, Name: name, Count: buckets[key], Future: day.After(today), Selected: c.Period == "day" && key == c.Date}
		c.Buckets = append(c.Buckets, b)
		maxCount = max(maxCount, b.Count)
		if c.Period == "year" {
			day = day.AddDate(0, 1, 0)
		} else {
			day = day.AddDate(0, 0, 1)
		}
	}
	for i := range c.Buckets {
		if c.Buckets[i].Count > 0 {
			c.Buckets[i].Height = max(2, c.Buckets[i].Count*100/maxCount)
		}
	}
	p.ChoreChart = c
	p.Title = "Chore charts"
	return nil
}

// Keep older chart bookmarks on the combined Chores page.
func (a *App) choreChartsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if person := q.Get("person"); person != "" {
		q.Set("chart_person", person)
		q.Del("person")
	}
	http.Redirect(w, r, "/chores?"+q.Encode()+"#chore-charts", http.StatusSeeOther)
}

func (a *App) loadEmbeddedChoreCharts(p *Page, q url.Values) error {
	chartQuery := url.Values{"period": {q.Get("period")}, "date": {q.Get("date")}, "person": {q.Get("chart_person")}}
	if err := a.loadChoreCharts(p, chartQuery); err != nil {
		return err
	}
	// Chart navigation retains the independent list filters and normal form returns.
	withList := func(raw string) string {
		if raw == "" {
			return ""
		}
		u, _ := url.Parse(raw)
		values := u.Query()
		for _, key := range []string{"person", "routine", "schedules"} {
			if value := q.Get(key); value != "" {
				values.Set(key, value)
			}
		}
		u.RawQuery = values.Encode()
		return u.String()
	}
	c := &p.ChoreChart
	for _, target := range []*string{&c.PrevURL, &c.NextURL, &c.CurrentURL, &c.DayURL, &c.MonthURL, &c.YearURL} {
		*target = withList(*target)
	}
	for i := range c.People {
		c.People[i].URL = withList(c.People[i].URL)
	}
	p.Return = withList(p.Return)
	p.Title = "Chores"
	return nil
}
