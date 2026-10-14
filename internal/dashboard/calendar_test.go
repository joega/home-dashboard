package dashboard

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDayCalendarSharesAllHouseholdAssignments(t *testing.T) {
	s, _ := testStore(t)
	for _, name := range []string{"Alex", "Blair", "Casey", "Drew"} {
		if err := s.SavePerson(Person{Name: name, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	people, err := s.People()
	if err != nil {
		t.Fatal(err)
	}
	for i, person := range people {
		for _, start := range []string{"", []string{"09:00", "10:00", "11:00", "12:00"}[i]} {
			if err := s.Save(Entry{Kind: "event", Title: person.Name + " plan " + start, PersonID: person.ID, Owner: person.Name, Date: "2026-10-14", Start: start}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Save(Entry{Kind: "event", Title: "Family dinner", Owner: "Household", Date: "2026-10-14", Start: "18:00"}); err != nil {
		t.Fatal(err)
	}
	// Recurring shared events must also appear; tomorrow's entries must not leak.
	if err := s.Save(Entry{Kind: "event", Title: "Daily check-in", Owner: "Anyone", Date: "2026-10-13", Start: "08:00", Repeat: "daily", RepeatEvery: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Entry{Kind: "event", Title: "Tomorrow only", Date: "2026-10-15", Start: "15:00"}); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/New_York")
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.List("event", "2026-10-14")
	if err != nil {
		t.Fatal(err)
	}
	p := Page{Now: time.Date(2026, 10, 14, 10, 0, 0, 0, loc), Today: "2026-10-14", Events: events}
	if err = a.loadCalendar(&p, url.Values{"mode": {"day"}, "date": {"2026-10-14"}}); err != nil {
		t.Fatal(err)
	}
	if len(p.Lanes) != 1 || p.Lanes[0].Label != "Everyone" || len(p.Lanes[0].AllDay) != 4 || len(p.Lanes[0].Events) != 6 || len(p.Lanes[0].Blocks) != 6 {
		t.Fatalf("shared day lost events: %#v", p.Lanes)
	}
	for _, b := range p.Lanes[0].Blocks {
		if b.Width != 100 || b.More != 0 {
			t.Fatal("nonoverlapping event lost full width", b)
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/calendar?mode=day&date=2026-10-14", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `timeline-lanes lane-count-1`) || strings.Contains(body, "Tomorrow only") {
		t.Fatal("shared day failed to render", body)
	}
	for _, person := range people {
		if !strings.Contains(body, person.Name+" plan") || strings.Count(body, `title="Assigned to `+person.Name+`"`) != 2 {
			t.Fatalf("missing timed/all-day badge for %s", person.Name)
		}
	}
	if !strings.Contains(body, "Family dinner") || !strings.Contains(body, "Daily check-in") || strings.Contains(body, `title="Assigned to Household"`) || strings.Contains(body, `title="Assigned to Anyone"`) {
		t.Fatal("shared events missing or incorrectly assigned", body)
	}
}
