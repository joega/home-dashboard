package dashboard

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecurrenceCalendarDatesAndBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		e       Entry
		yes, no []string
	}{
		{"weekday school term", Entry{Date: "2026-10-13", Repeat: "weekdays", RepeatUntil: "2026-10-23"}, []string{"2026-10-13", "2026-10-16", "2026-10-19", "2026-10-23"}, []string{"2026-10-12", "2026-10-17", "2026-10-18", "2026-10-26"}},
		{"every other day through DST", Entry{Date: "2026-03-06", Repeat: "daily", RepeatEvery: 2}, []string{"2026-03-06", "2026-03-08", "2026-03-10"}, []string{"2026-03-07", "2026-03-09"}},
		{"fortnightly", Entry{Date: "2026-10-13", Repeat: "weekly", RepeatEvery: 2}, []string{"2026-10-13", "2026-10-27", "2026-11-10"}, []string{"2026-10-20", "2026-10-28"}},
		{"selected days fortnightly", Entry{Date: "2026-10-13", Repeat: "custom", RepeatEvery: 2, RepeatDays: (1 << 1) | (1 << 3)}, []string{"2026-10-14", "2026-10-26", "2026-10-28"}, []string{"2026-10-12", "2026-10-19", "2026-10-21", "2026-10-27"}},
		{"month end", Entry{Date: "2026-01-31", Repeat: "monthly"}, []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30"}, []string{"2026-02-27", "2026-03-30"}},
		{"alternate month end", Entry{Date: "2026-01-31", Repeat: "monthly", RepeatEvery: 2}, []string{"2026-03-31", "2026-05-31"}, []string{"2026-02-28", "2026-04-30"}},
		{"leap anniversary", Entry{Date: "2024-02-29", Repeat: "yearly"}, []string{"2025-02-28", "2026-02-28", "2028-02-29"}, []string{"2025-03-01", "2028-02-28"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, date := range c.yes {
				d, _ := calendarDate(date)
				if !matchesDate(c.e, d) {
					t.Error("missing occurrence", date)
				}
			}
			for _, date := range c.no {
				d, _ := calendarDate(date)
				if matchesDate(c.e, d) {
					t.Error("unexpected occurrence", date)
				}
			}
		})
	}
	loc, _ := time.LoadLocation("America/New_York")
	events := []Entry{{Kind: "event", Title: "Early call", Date: "2026-03-01", Start: "02:30", Repeat: "weekly"}}
	if len(eventsOn(events, "2026-03-08", loc)) != 0 || len(eventsOn(events, "2026-03-15", loc)) != 1 {
		t.Fatal("nonexistent DST time handling failed")
	}
}
func TestScheduledChoresCompletionAndRestart(t *testing.T) {
	s, path := testStore(t)
	if err := s.Save(Entry{Kind: "chore", Title: "School bag", Routine: "Morning", Date: "2026-10-13", Repeat: "weekdays"}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List("chore", "2026-10-13")
	id := list[0].ID
	if !list[0].Due || !list[0].Daily {
		t.Fatal("scheduled weekday chore not due")
	}
	s.SetDone(id, "2026-10-13", true)
	next, _ := s.Get(id, "2026-10-14")
	if next.Done || !next.Due {
		t.Fatal("completion leaked to next occurrence")
	}
	weekend, _ := s.Get(id, "2026-10-17")
	if weekend.Due {
		t.Fatal("weekday chore appeared on weekend")
	}
	if err := s.SetDone(id, "2026-10-17", true); !errors.Is(err, ErrNotDue) {
		t.Fatal("off-schedule completion accepted", err)
	}
	s.Close()
	s, err := Open(path, false, "2026-10-14")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	past, _ := s.Get(id, "2026-10-13")
	if !past.Done || past.Repeat != "weekdays" {
		t.Fatal("restart lost recurrence/completion")
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM chore_history").Scan(&n)
	if n != 1 {
		t.Fatal("history included unscheduled completion")
	}
	s.Save(Entry{Kind: "chore", Title: "Future once", Routine: "Evening", Date: "2026-11-01"})
	list, _ = s.List("chore", "2026-10-13")
	if list[1].Due {
		t.Fatal("future one-time chore appeared early")
	}
	list, _ = s.List("chore", "2026-11-02")
	if !list[1].Due {
		t.Fatal("one-time chore disappeared after its start date")
	}
}
func TestRecurringEventsEveryViewAndLocation(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	e := Entry{Kind: "event", Title: "School pickup", Date: "2026-10-13", Start: "14:30", End: "14:45", Repeat: "weekdays", RepeatUntil: "2026-10-23", Location: "<school entrance>"}
	if err := s.Save(e); err != nil {
		t.Fatal(err)
	}
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := s.List("event", "2026-10-13")
	if len(raw) != 1 {
		t.Fatal("recurrence was materialized as duplicated rows")
	}
	for _, date := range []string{"2026-10-14", "2026-10-16", "2026-10-19", "2026-10-23"} {
		events := eventsOn(raw, date, loc)
		if len(events) != 1 || events[0].ID != raw[0].ID || events[0].Date != date || events[0].Location != e.Location {
			t.Fatal("occurrence metadata lost", date, events)
		}
	}
	for _, mode := range []string{"day", "3day", "week", "month"} {
		p := Page{Now: time.Date(2026, 10, 13, 11, 0, 0, 0, loc), Today: "2026-10-13", Events: raw}
		if err := a.loadCalendar(&p, url.Values{"mode": {mode}, "date": {"2026-10-13"}}); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, lane := range p.Lanes {
			count += len(lane.Events) + len(lane.AllDay)
		}
		for _, cell := range p.Cells {
			count += len(cell.Events)
		}
		want := map[string]int{"day": 1, "3day": 3, "week": 5, "month": 9}[mode]
		if count != want {
			t.Fatalf("%s occurrences = %d, want %d", mode, count, want)
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/calendar?mode=week&date=2026-10-13", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "&lt;school entrance&gt;") || strings.Contains(w.Body.String(), "<school entrance>") {
		t.Fatal("location rendering/escaping failed", w.Body.String())
	}
	if got := eventsOn(raw, "2026-10-26", loc); len(got) != 0 {
		t.Fatal("ended series continued")
	}
	p := Page{Now: time.Date(2026, 10, 14, 8, 30, 0, 0, loc), Today: "2026-10-14", View: "today"}
	if err := a.load(&p, nil); err != nil || len(p.Events) != 1 {
		t.Fatal("Today did not show recurring event", err)
	}
	if raw[0].Date != "2026-10-13" {
		t.Fatal("expansion mutated original anchor")
	}
}
func TestLegacyDailyMigrationAndScheduleValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "household.sqlite")
	s, err := Open(path, true, "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	// Emulate the pre-recurrence database marker/values without changing its completion state.
	s.db.Exec("UPDATE entries SET repeat='' WHERE kind='chore'")
	s.db.Exec("DELETE FROM settings WHERE key='recurrence_v1'")
	chores, _ := s.List("chore", "2026-10-13")
	s.SetDone(chores[0].ID, "2026-10-13", true)
	s.Close()
	s, err = Open(path, false, "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	chores, _ = s.List("chore", "2026-10-13")
	if chores[0].Repeat != "daily" || !chores[0].Done {
		t.Fatal("legacy daily schedule/completion lost")
	}
	loc, _ := time.LoadLocation("America/New_York")
	for _, e := range []Entry{
		{Kind: "event", Title: "E", Date: "2026-10-13", Repeat: "custom"},
		{Kind: "event", Title: "E", Date: "2026-10-13", Repeat: "daily", RepeatUntil: "2026-10-12"},
		{Kind: "event", Title: "E", Date: "2026-10-13", Repeat: "weekly", RepeatEvery: 100},
		{Kind: "event", Title: "E", Date: "2026-10-13", Repeat: "unknown"},
		{Kind: "event", Title: "E", Date: "2026-10-13", Location: strings.Repeat("x", 161)},
	} {
		if Validate(e, loc) == nil {
			t.Fatal("invalid recurrence/location accepted", e)
		}
	}
	if _, _, _, err := parseSchedule("daily", "0", nil); err == nil {
		t.Fatal("zero interval accepted")
	}
}
