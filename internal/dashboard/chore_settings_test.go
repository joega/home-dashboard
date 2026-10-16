package dashboard

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestChoreEligibilityDefaultsPersistenceAndAtomicSave(t *testing.T) {
	s, path := testStore(t)
	adult, child := notePeople(t, s)
	if !adult.ChoreEligible || !child.ChoreEligible {
		t.Fatal("existing people must default to eligible")
	}
	if err := s.SaveChorePeople([]int64{adult.ID, child.ID}, []int64{child.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveChorePeople([]int64{child.ID, 999}, nil); err == nil {
		t.Fatal("unknown roster accepted")
	}
	if p, _ := s.Person(child.ID); !p.ChoreEligible {
		t.Fatal("failed save partially committed")
	}
	if err := s.SaveChorePeople([]int64{child.ID}, []int64{adult.ID}); err == nil {
		t.Fatal("unlisted selection accepted")
	}
	adult.Name = "Alex renamed"
	if err := s.SavePerson(adult); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePerson(Person{Name: "New child"}); err != nil {
		t.Fatal(err)
	}
	people, _ := s.People()
	for _, p := range people {
		if p.Name == "New child" && !p.ChoreEligible {
			t.Fatal("new person not eligible by default")
		}
	}
	s.Close()
	reopened, err := Open(path, false, "2026-10-16")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	p, err := reopened.Person(adult.ID)
	if err != nil || p.ChoreEligible || !p.Active || p.Name != "Alex renamed" {
		t.Fatal("eligibility lost or household profile changed", p, err)
	}
	if err := reopened.SaveChorePeople([]int64{adult.ID, child.ID}, nil); err != nil {
		t.Fatal("all unchecked should be allowed", err)
	}
	if p, _ := reopened.Person(child.ID); p.ChoreEligible {
		t.Fatal("all unchecked did not save")
	}
}

func TestChoreEligibilityViewsCreditAndHistory(t *testing.T) {
	s, _ := testStore(t)
	adult, child := notePeople(t, s)
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 10, 16, 11, 0, 0, 0, loc)
	day := now.Format(time.DateOnly)
	for _, e := range []Entry{{Kind: "chore", Title: "Adult assigned job", PersonID: adult.ID, Owner: adult.Name, Date: day}, {Kind: "chore", Title: "Child assigned job", PersonID: child.ID, Owner: child.Name, Date: day}, {Kind: "event", Title: "Adult calendar event", PersonID: adult.ID, Owner: adult.Name, Date: day, Start: "14:00"}} {
		if err := s.Save(e); err != nil {
			t.Fatal(err)
		}
	}
	addChartStar(t, s, adult, day, now)
	addChartStar(t, s, child, day, now)
	addChartStar(t, s, Person{}, day, now)
	if err := s.SaveChorePeople([]int64{adult.ID, child.ID}, []int64{child.ID}); err != nil {
		t.Fatal(err)
	}
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	p := Page{View: "chores", Now: now, Today: day}
	if err := a.load(&p, url.Values{"person": {strconv.FormatInt(adult.ID, 10)}, "chart_person": {strconv.FormatInt(adult.ID, 10)}}); err != nil {
		t.Fatal(err)
	}
	if len(p.ChorePeople) != 1 || p.ChorePeople[0].ID != child.ID || p.PersonFilter != "" || p.ChoreChart.Person != "" {
		t.Fatal("excluded person available through filters")
	}
	if p.ChoreChart.Count != 2 || p.ChoreChart.Helpers != 1 || p.ChoreChart.Buckets[15].Count != 2 {
		t.Fatal("adult stars still counted", p.ChoreChart)
	}
	for _, person := range p.ChoreChart.People {
		if person.Person.ID == adult.ID {
			t.Fatal("excluded chart row")
		}
	}
	if len(p.Chores) != 2 {
		t.Fatal("existing chores disappeared")
	}
	var adultJob Entry
	for _, e := range p.Chores {
		if e.Title == "Adult assigned job" {
			adultJob = e
		}
	}
	if adultJob.PersonID != 0 || adultJob.Owner != "Household" {
		t.Fatal("excluded assignment not shared", adultJob)
	}
	stored, err := s.Get(adultJob.ID, day)
	if err != nil || stored.PersonID != adult.ID {
		t.Fatal("saved assignment was rewritten", stored, err)
	}
	today := Page{View: "today", Now: now, Today: day}
	if err := a.load(&today, nil); err != nil {
		t.Fatal(err)
	}
	if len(today.ChoreSummary) != 2 || today.ChoreSummary[0].Name != "Household" {
		t.Fatal("Today has excluded assignee", today.ChoreSummary)
	}
	calendar := Page{View: "calendar", Now: now, Today: day}
	if err := a.load(&calendar, nil); err != nil {
		t.Fatal(err)
	}
	if calendar.Lanes[0].Events[0].PersonID != adult.ID {
		t.Fatal("calendar assignment changed")
	}
	// Stale/global adult identity must record Household credit, rather than vanish from analytics.
	if err := s.SetDoneAs(adultJob.ID, day, true, now, adult); err != nil {
		t.Fatal(err)
	}
	c := readChoreChart(t, a, now, url.Values{})
	if c.Count != 3 || chartPerson(t, c, 0).Count != 2 {
		t.Fatal("adult participation not credited to Household", c)
	}
	var ledgerCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM chore_history WHERE person_id=?", adult.ID).Scan(&ledgerCount); err != nil || ledgerCount != 1 {
		t.Fatal("adult history changed", ledgerCount, err)
	}
	if err := s.SaveChorePeople([]int64{adult.ID}, []int64{adult.ID}); err != nil {
		t.Fatal(err)
	}
	c = readChoreChart(t, a, now, url.Values{})
	if c.Count != 4 || chartPerson(t, c, adult.ID).Count != 1 {
		t.Fatal("re-enabled history did not return", c)
	}
}

func TestChoreSettingsHTTPAssignmentsAndCSRF(t *testing.T) {
	s, _ := testStore(t)
	adult, child := notePeople(t, s)
	a, err := New(s, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	request := func(method, path string, values url.Values, asAdult bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://household.test"+path, strings.NewReader(values.Encode()))
		if method == "POST" {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.Header.Set("HX-Request", "true")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if asAdult {
			r.AddCookie(&http.Cookie{Name: "hq_person", Value: strconv.FormatInt(adult.ID, 10)})
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	first := request("GET", "/settings?section=chores", nil, false)
	if first.Code != 200 || !strings.Contains(first.Body.String(), "Chore participants") {
		t.Fatal(first.Code, first.Body.String())
	}
	cookie = first.Result().Cookies()[0]
	values := url.Values{"person_id": {strconv.FormatInt(adult.ID, 10), strconv.FormatInt(child.ID, 10)}, "eligible": {strconv.FormatInt(child.ID, 10)}}
	if w := request("POST", "/settings/chores", values, false); w.Code != 403 {
		t.Fatal("CSRF bypass", w.Code)
	}
	values.Set("csrf", cookie.Value)
	w := request("POST", "/settings/chores", values, false)
	if recipeLocation(w) != "/settings?section=chores&saved=1" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	adultOption := `<option value="` + strconv.FormatInt(adult.ID, 10) + `"`
	childOption := `<option value="` + strconv.FormatInt(child.ID, 10) + `"`
	body := request("GET", "/forms/chore/new", nil, true).Body.String()
	if strings.Contains(body, adultOption) || !strings.Contains(body, childOption) || !strings.Contains(body, `value="0" selected`) {
		t.Fatal("adult default/assignment still available", body)
	}
	body = request("GET", "/forms/event/new", nil, true).Body.String()
	if !strings.Contains(body, adultOption+" selected") {
		t.Fatal("adult calendar default removed")
	}
	body = request("GET", "/current-person", nil, false).Body.String()
	if !strings.Contains(body, adult.Name) {
		t.Fatal("adult household profile removed")
	}
	forged := url.Values{"csrf": {cookie.Value}, "person_id": {strconv.FormatInt(adult.ID, 10)}, "title": {"Forged adult chore"}, "date": {"2026-10-16"}, "repeat": {""}}
	w = request("POST", "/entries/chore/0", forged, false)
	if !strings.Contains(w.Body.String(), "not enabled for chores") {
		t.Fatal("ineligible assignment accepted", w.Body.String())
	}
	chores, _ := s.List("chore", "2026-10-16")
	if len(chores) != 0 {
		t.Fatal("invalid chore saved")
	}
	values.Set("eligible", "not-an-id")
	w = request("POST", "/settings/chores", values, false)
	if !strings.Contains(w.Body.String(), "Choose people from the checklist.") || w.Header().Get("HX-Push-Url") != "/settings?section=chores" {
		t.Fatal("invalid settings failed unsafely", w.Header())
	}
}
