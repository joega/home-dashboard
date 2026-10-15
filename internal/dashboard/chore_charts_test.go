package dashboard

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func readChoreChart(t *testing.T, a *App, now time.Time, q url.Values) ChoreChart {
	t.Helper()
	p := Page{View: "chore-charts", Now: now.In(a.loc), Today: now.In(a.loc).Format(time.DateOnly)}
	if err := a.load(&p, q); err != nil {
		t.Fatal(err)
	}
	return p.ChoreChart
}
func chartPerson(t *testing.T, c ChoreChart, id int64) ChoreChartPerson {
	t.Helper()
	for _, p := range c.People {
		if p.Person.ID == id {
			return p
		}
	}
	t.Fatalf("missing person %d in %#v", id, c.People)
	return ChoreChartPerson{}
}
func addChartStar(t *testing.T, s *Store, person Person, day string, at time.Time) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO chore_history(entry_key,entry_id,cycle,day,completed_at,person_id,person_name,title) VALUES(lower(hex(randomblob(16))),1,'once',?,?,?,?,?)`, day, at.UTC().Format(time.RFC3339Nano), personRef(person.ID), person.Name, "A recorded job")
	if err != nil {
		t.Fatal(err)
	}
}
func TestChoreChartsUseRetainedCreditsNotAssigneesOrAudit(t *testing.T) {
	s, _ := testStore(t)
	alex, sam := notePeople(t, s)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e := Entry{Kind: "chore", Title: "Wash dishes", PersonID: alex.ID, Owner: alex.Name, Daily: true, Repeat: "daily", RepeatEvery: 1, Date: "2026-10-01"}
	if err := s.SaveAs(e, alex, now); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List("chore", "2026-10-14")
	e = list[0]
	for range 2 {
		if err := s.SetDoneAs(e.ID, "2026-10-13", true, now, sam); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetDoneAs(e.ID, "2026-10-14", true, now, Person{}); err != nil {
		t.Fatal(err)
	}
	// Shopping activity must never earn chore stars.
	if err := s.SaveAs(Entry{Kind: "shopping", Title: "Milk"}, sam, now); err != nil {
		t.Fatal(err)
	}
	shopping, _ := s.List("shopping", "2026-10-14")
	if err := s.SetDoneAs(shopping[0].ID, "2026-10-14", true, now, sam); err != nil {
		t.Fatal(err)
	}
	a, err := New(s, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	c := readChoreChart(t, a, now, url.Values{})
	if c.Count != 2 || c.Helpers != 1 || chartPerson(t, c, sam.ID).Count != 1 || chartPerson(t, c, alex.ID).Count != 0 || chartPerson(t, c, 0).Count != 1 {
		t.Fatal("credit or duplicate-count mismatch", c)
	}
	// Reopening appends audit activity but retracts exactly one valid credit.
	if err := s.SetDoneAs(e.ID, "2026-10-13", false, now, alex); err != nil {
		t.Fatal(err)
	}
	c = readChoreChart(t, a, now, url.Values{})
	if c.Count != 1 || chartPerson(t, c, sam.ID).Count != 0 {
		t.Fatal("reopened occurrence still counted", c)
	}
	if err := s.SetDoneAs(e.ID, "2026-10-13", true, now, sam); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(e.ID); err != nil {
		t.Fatal(err)
	}
	sam.Name = "Sam renamed"
	if err := s.SavePerson(sam); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPersonActive(sam.ID, false); err != nil {
		t.Fatal(err)
	}
	c = readChoreChart(t, a, now, url.Values{})
	row := chartPerson(t, c, sam.ID)
	if c.Count != 2 || row.Count != 1 || row.Person.Name != "Sam renamed" || row.Person.Active {
		t.Fatal("deletion/archive/rename erased or moved history", c)
	}
	// Legacy completions with no credit ledger are not guessed retroactively.
	if _, err = s.db.Exec(`INSERT INTO entries(kind,title,done,entry_key) VALUES('chore','Old completed task',1,'legacy-chart-test')`); err != nil {
		t.Fatal(err)
	}
	if after := readChoreChart(t, a, now, url.Values{}); after.Count != 2 {
		t.Fatal("legacy completion invented a credit", after)
	}
}
func TestChoreChartPeriodsLeapDayFilteringAndMilestones(t *testing.T) {
	s, _ := testStore(t)
	alex, sam := notePeople(t, s)
	zone, _ := time.LoadLocation("America/New_York")
	now := time.Date(2024, 3, 1, 12, 0, 0, 0, zone)
	for day, n := range map[string]int{"2024-02-27": 1, "2024-02-28": 2, "2024-02-29": 2, "2024-03-01": 3, "2023-12-31": 1} {
		for range n {
			addChartStar(t, s, sam, day, time.Date(2024, 3, 1, 2, 30, 0, 0, time.UTC))
		}
	}
	addChartStar(t, s, Person{}, "2024-02-29", now)
	a, err := New(s, zone)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"period": {"month"}, "date": {"2024-02-29"}}
	month := readChoreChart(t, a, now, q)
	row := chartPerson(t, month, sam.ID)
	if month.Count != 6 || month.Helpers != 1 || len(month.Buckets) != 29 || month.Buckets[28].Count != 3 || row.Count != 5 || row.BestRun != 3 || row.Badge != "Helping hand" || row.NextBadge != 10 {
		t.Fatal("February grouping/badge", month, row)
	}
	q.Set("person", strconv.FormatInt(sam.ID, 10))
	filtered := readChoreChart(t, a, now, q)
	if filtered.Count != 5 || len(filtered.People) != 1 || filtered.Buckets[28].Count != 2 || filtered.Scope != sam.Name {
		t.Fatal("person filter", filtered)
	}
	q.Set("person", "household")
	shared := readChoreChart(t, a, now, q)
	if shared.Count != 1 || shared.Helpers != 0 || len(shared.People) != 1 || shared.People[0].Person.ID != 0 || shared.People[0].Badge != "Shared stars" {
		t.Fatal("shared credit became personal", shared)
	}
	q.Set("person", strconv.FormatInt(alex.ID, 10))
	zero := readChoreChart(t, a, now, q)
	if zero.Count != 0 || len(zero.People) != 1 || zero.People[0].Count != 0 || zero.People[0].NextBadge != 1 {
		t.Fatal("empty person", zero)
	}
	q.Del("person")
	q.Set("period", "day")
	day := readChoreChart(t, a, now, q)
	if day.Count != 3 || len(day.Buckets) != 7 || day.Buckets[6].Label != "Thu" || !day.Buckets[6].Selected {
		t.Fatal("day view", day)
	}
	total := 0
	for _, b := range day.Buckets {
		total += b.Count
	}
	if total != 6 {
		t.Fatal("seven-day context counted outside its range", day.Buckets)
	}
	q.Set("period", "year")
	year := readChoreChart(t, a, now, q)
	if year.Count != 9 || len(year.Buckets) != 12 || year.Buckets[1].Count != 6 || year.Buckets[2].Count != 3 || !year.Buckets[3].Future || chartPerson(t, year, sam.ID).BestRun != 4 {
		t.Fatal("year grouping/streak", year)
	}
	for _, c := range []ChoreChart{month, filtered, shared, zero, day, year} {
		for _, b := range c.Buckets {
			if b.Height < 0 || b.Height > 100 || b.Count == 0 && b.Height != 0 {
				t.Fatal("invalid bar", b)
			}
		}
	}
}
func TestChoreChartDSTBoundsAndReversibleBadges(t *testing.T) {
	s, _ := testStore(t)
	_, sam := notePeople(t, s)
	zone, _ := time.LoadLocation("America/New_York")
	now := time.Date(2024, 11, 5, 12, 0, 0, 0, zone)
	for _, day := range []string{"2024-11-02", "2024-11-03", "2024-11-04"} {
		addChartStar(t, s, sam, day, now)
	}
	a, err := New(s, zone)
	if err != nil {
		t.Fatal(err)
	}
	c := readChoreChart(t, a, now, url.Values{"period": {"month"}})
	if chartPerson(t, c, sam.ID).BestRun != 3 {
		t.Fatal("DST broke consecutive dates", c)
	}
	c = readChoreChart(t, a, now, url.Values{"period": {"day"}, "date": {"2024-11-04"}})
	if len(c.Buckets) != 7 || c.Buckets[0].Name != "Tue, Oct 29, 2024" || c.Buckets[6].Name != "Mon, Nov 4, 2024" || c.Count != 1 {
		t.Fatal("DST broke seven-day context", c)
	}
	invalid := readChoreChart(t, a, now, url.Values{"period": {"bogus"}, "date": {"2099-99-99"}, "person": {"//bad.test"}})
	if invalid.Period != "month" || invalid.Date != "2024-11-05" || invalid.Person != "" {
		t.Fatal("invalid query was not normalized", invalid)
	}
	future := readChoreChart(t, a, now, url.Values{"period": {"year"}, "date": {"2099-01-01"}})
	if future.Label != "2024" || future.NextURL != "" {
		t.Fatal("future date escaped current period", future)
	}
	oldest := readChoreChart(t, a, now, url.Values{"period": {"year"}, "date": {"1970-01-01"}})
	if oldest.PrevURL != "" || oldest.Count != 0 || len(oldest.Buckets) != 12 {
		t.Fatal("lower date boundary", oldest)
	}
	for i := 0; i < 7; i++ {
		addChartStar(t, s, sam, "2024-11-04", now)
	}
	c = readChoreChart(t, a, now, url.Values{})
	if row := chartPerson(t, c, sam.ID); row.Count != 10 || row.Badge != "Super helper" || row.NextBadge != 25 {
		t.Fatal("ten-star milestone", row)
	}
	if _, err := s.db.Exec(`DELETE FROM chore_history WHERE id=(SELECT MAX(id) FROM chore_history)`); err != nil {
		t.Fatal(err)
	}
	c = readChoreChart(t, a, now, url.Values{})
	if row := chartPerson(t, c, sam.ID); row.Count != 9 || row.Badge != "Helping hand" {
		t.Fatal("badge did not follow retracted credit", row)
	}
	for i := 0; i < 91; i++ {
		addChartStar(t, s, sam, "2024-11-04", now)
	}
	if row := chartPerson(t, readChoreChart(t, a, now, url.Values{}), sam.ID); row.Count != 100 || row.Badge != "Chore champion" || row.NextBadge != 0 {
		t.Fatal("top milestone", row)
	}
}
func TestChoreChartHTTPNavigationEscapingAndReadOnly(t *testing.T) {
	s, _ := testStore(t)
	if err := s.SavePerson(Person{Name: `Alex <b>helper</b>`}); err != nil {
		t.Fatal(err)
	}
	people, _ := s.People()
	person := people[0]
	now := time.Now().UTC()
	day := now.Format(time.DateOnly)
	addChartStar(t, s, person, day, now)
	a, err := New(s, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	for _, hx := range []bool{false, true} {
		req := httptest.NewRequest("GET", fmt.Sprintf("http://house.test/chores/charts?period=day&date=%s&person=%d", day, person.ID), nil)
		if hx {
			req.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, req)
		text := w.Body.String()
		if w.Code != 200 || !strings.Contains(text, `aria-label="Chore completion charts"`) || !strings.Contains(text, `aria-current="page" aria-label="Chores"`) || !strings.Contains(text, `Alex &lt;b&gt;helper&lt;/b&gt;`) || strings.Contains(text, `Alex <b>helper</b>`) {
			t.Fatal("chart rendering or escaping", w.Code, text)
		}
		if hx && (w.Header().Get("HX-Push-Url") != choreChartURL("day", day, strconv.FormatInt(person.ID, 10)) || strings.Contains(text, "<!doctype")) {
			t.Fatal("fragment URL", w.Header())
		}
		if !hx && !strings.Contains(text, "<!doctype") {
			t.Fatal("full page missing")
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://house.test/chores", nil))
	if !strings.Contains(w.Body.String(), `class="chore-charts-link" href="/chores/charts"`) {
		t.Fatal("Charts not linked from Chores")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM chore_history").Scan(&count); err != nil || count != 1 {
		t.Fatal("viewing charts changed credits", count, err)
	}
	target := choreChartURL("year", day, strconv.FormatInt(person.ID, 10))
	if safeProfileReturn(target) != target {
		t.Fatal("profile switch loses chart view")
	}
}

func TestChoreChartEmptyHouseholdDoesNotInventProgress(t *testing.T) {
	s, _ := testStore(t)
	a, err := New(s, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	c := readChoreChart(t, a, now, url.Values{})
	if c.Count != 0 || c.Helpers != 0 || len(c.People) != 0 {
		t.Fatal("empty household invented achievements", c)
	}
	for _, b := range c.Buckets {
		if b.Count != 0 || b.Height != 0 {
			t.Fatal("empty chart invented a bar", b)
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://house.test/chores/charts", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Add people") || !strings.Contains(w.Body.String(), "No completions recorded") {
		t.Fatal("empty household has no onboarding", w.Code, w.Body.String())
	}
}
