package dashboard

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPendingGroupsCountsAndStableLinks(t *testing.T) {
	chores := []Entry{{Due: true, Owner: "Anyone"}, {Due: true, Owner: "Household"}, {Due: true, PersonID: 7, Owner: "Zoe"}, {Due: true, PersonID: 8, Owner: "Alex"}, {Due: true, Done: true, PersonID: 7, Owner: "Zoe"}, {Due: false, PersonID: 8, Owner: "Alex"}}
	groups, total := pendingGroups(chores, true)
	if total != 4 || len(groups) != 3 || groups[0].Name != "Household" || groups[0].Count != 2 || groups[0].URL != "/chores?person=none" || groups[1].Name != "Alex" || groups[1].URL != "/chores?person=8" {
		t.Fatal(groups, total)
	}
	shopping := []Entry{{Quantity: "10"}, {ShopID: 4, ShopName: "A & B"}, {ShopID: 4, ShopName: "A & B", Done: true}, {ShopID: 4, ShopName: "A & B"}}
	groups, total = pendingGroups(shopping, false)
	if total != 3 || len(groups) != 2 || groups[0].Count != 1 || groups[1].Count != 2 || groups[1].URL != "/shopping?store=4" {
		t.Fatal(groups, total)
	}
	if groups, total = pendingGroups(nil, true); len(groups) != 0 || total != 0 {
		t.Fatal(groups, total)
	}
}
func TestTodayDrilldownAndChorePersonFilter(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	day := time.Now().In(loc).Format("2006-01-02")
	if err := s.SavePerson(Person{Name: "Alex"}); err != nil {
		t.Fatal(err)
	}
	people, _ := s.People()
	id := people[0].ID
	for _, e := range []Entry{{Kind: "chore", Title: "Alex task", Owner: "Alex", PersonID: id, Date: day, Routine: "Morning"}, {Kind: "chore", Title: "Unassigned task", Owner: "Anyone", Date: day}, {Kind: "chore", Title: "Future task", Owner: "Alex", PersonID: id, Date: "2099-01-01"}, {Kind: "shopping", Title: "Milk"}} {
		if err := s.Save(e); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) string {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test"+path, nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	body := get("/")
	if !strings.Contains(body, "Alex: 1 pending chore") || !strings.Contains(body, "Household: 1 pending chore") || !strings.Contains(body, "Any store: 1 shopping item needed") || strings.Contains(body, "Complete Alex task") {
		t.Fatal("incorrect overview", body)
	}
	body = get("/chores?person=" + strconv.FormatInt(id, 10))
	if !strings.Contains(body, "Alex task") || strings.Contains(body, "Unassigned task") || strings.Contains(body, "Future task") || !strings.Contains(body, "person="+strconv.FormatInt(id, 10)+"&amp;routine=Morning") {
		t.Fatal("filter or preserved routine link incorrect", body)
	}
	body = get("/chores?person=none")
	if strings.Contains(body, "Alex task") || !strings.Contains(body, "Unassigned task") {
		t.Fatal("unassigned filter incorrect")
	}
	if err = s.SetPersonActive(id, false); err != nil {
		t.Fatal(err)
	}
	body = get("/chores?person=" + strconv.FormatInt(id, 10) + "&schedules=all")
	if !strings.Contains(body, "Future task") || !strings.Contains(body, "Alex (archived)") {
		t.Fatal("archived assignment or all-schedules filter lost")
	}
	body = get("/chores?person=9999")
	if !strings.Contains(body, "Unassigned task") || !strings.Contains(body, "Alex task") {
		t.Fatal("invalid filter did not recover to Everyone")
	}
}

func TestTodayCalendarSummaryIsReadOnly(t *testing.T) {
	s, _ := testStore(t)
	s.db.Exec("UPDATE settings SET value='true' WHERE key='demo'")
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Now().In(loc)
	day := now.Format("2006-01-02")
	for _, e := range []Entry{
		{Kind: "event", Title: "Today appointment", Date: day, Start: "23:59"},
		{Kind: "event", Title: "Daily pickup", Date: now.AddDate(0, 0, -1).Format("2006-01-02"), Start: "23:59", Repeat: "daily", RepeatEvery: 1},
		{Kind: "event", Title: "Future appointment", Date: now.AddDate(0, 0, 1).Format("2006-01-02"), Start: "09:00"},
	} {
		if e := s.Save(e); e != nil {
			t.Fatal(e)
		}
	}
	a, e := New(s, loc)
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/", nil))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, ">2 events ") || !strings.Contains(body, `href="/calendar?mode=day&amp;date=`+day+`"`) || strings.Contains(body, "Future appointment") {
		t.Fatal("incorrect calendar count/link")
	}
	if strings.Contains(body, "New event") || strings.Contains(body, `hx-get="/forms/event/`) || strings.Contains(body, `hx-post="/reset-demo"`) {
		t.Fatal("Today exposes calendar editing")
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/calendar?mode=day&date="+day, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Today appointment") || !strings.Contains(w.Body.String(), "Daily pickup") {
		t.Fatal("calendar drilldown lost today's events")
	}
}

func TestRemainingTodayEventsUsesEndTime(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{
		{ID: 1, Start: "07:50", End: "08:10"},
		{ID: 2, Start: "09:30"}, // The calendar gives events without an end one hour.
		{ID: 3, Start: "10:00", End: "11:00"},
		{ID: 4, Start: "23:30"},
		{ID: 5}, // All-day events remain visible.
	}
	for _, tc := range []struct{ at, want string }{
		{"07:49:00", "1,2,3,4,5"},
		{"08:09:59", "1,2,3,4,5"},
		{"08:10:00", "2,3,4,5"},
		{"10:29:59", "2,3,4,5"},
		{"10:30:00", "3,4,5"},
		{"10:59:59", "3,4,5"},
		{"11:00:00", "4,5"},
		{"23:59:59", "4,5"},
	} {
		t.Run(tc.at, func(t *testing.T) {
			now, err := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-16 "+tc.at, loc)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, e := range remainingTodayEvents(entries, now) {
				ids = append(ids, strconv.FormatInt(e.ID, 10))
			}
			if got := strings.Join(ids, ","); got != tc.want {
				t.Fatalf("remaining = %s, want %s", got, tc.want)
			}
		})
	}
	if len(entries) != 5 || entries[0].ID != 1 {
		t.Fatal("filter mutated original calendar entries")
	}
}

func TestTodayExpiredEventsCountHighlightAndCalendarHistory(t *testing.T) {
	s, _ := testStore(t)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entry{
		{Kind: "event", Title: "School Dropoff", Date: "2026-10-15", Start: "07:50", End: "08:10", Repeat: "daily", RepeatEvery: 1},
		{Kind: "event", Title: "School Pickup", Date: "2026-10-16", Start: "14:35", End: "14:50"},
		{Kind: "event", Title: "Karate", Date: "2026-10-16", Start: "16:00", End: "17:00"},
		{Kind: "event", Title: "Bring Bike Inside", Date: "2026-10-16", Start: "18:30"},
		{Kind: "event", Title: "Tomorrow Only", Date: "2026-10-17", Start: "07:00"},
	} {
		if err := s.Save(e); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at, first string
		count     int
	}{
		{"08:00", "School Dropoff", 4},
		{"08:10", "School Pickup", 3},
		{"10:59", "School Pickup", 3},
		{"14:40", "School Pickup", 3},
		{"14:50", "Karate", 2},
		{"17:00", "Bring Bike Inside", 1},
		{"19:30", "", 0},
	} {
		t.Run(tc.at, func(t *testing.T) {
			local, err := time.ParseInLocation("2006-01-02 15:04", "2026-10-16 "+tc.at, loc)
			if err != nil {
				t.Fatal(err)
			}
			// The clock can arrive in UTC; filtering must use the household zone.
			p := Page{Now: local.UTC(), Today: "2026-10-16", Zone: loc.String(), View: "today"}
			if err := a.load(&p, nil); err != nil {
				t.Fatal(err)
			}
			if len(p.Events) != tc.count {
				t.Fatalf("got %d events, want %d", len(p.Events), tc.count)
			}
			w := httptest.NewRecorder()
			a.render(w, "today", p)
			body := w.Body.String()
			count := strconv.Itoa(tc.count) + " "
			if !strings.Contains(body, count+"event") || strings.Contains(body, "Tomorrow Only") {
				t.Fatal("incorrect count or date")
			}
			if tc.count == 0 {
				if !strings.Contains(body, "No more events today.") || strings.Contains(body, "event-summary") {
					t.Fatal("incorrect empty state")
				}
				return
			}
			if p.Events[0].Title != tc.first {
				t.Fatal("wrong highlighted event", p.Events[0].Title)
			}
			featured := strings.Index(body, `class="event-row event-summary featured"`)
			if featured < 0 || !strings.Contains(strings.SplitN(body[featured:], "</a>", 2)[0], tc.first) {
				t.Fatal("first remaining event not highlighted")
			}
			if tc.first != "School Dropoff" && strings.Contains(body, "School Dropoff") {
				t.Fatal("ended event remains visible")
			}
		})
	}
	// Past events stay in the full calendar, including today's recurring occurrence.
	p := Page{Now: time.Date(2026, 10, 16, 19, 30, 0, 0, loc), Today: "2026-10-16", View: "calendar"}
	if err := a.load(&p, nil); err != nil {
		t.Fatal(err)
	}
	if len(p.Lanes) != 1 || len(p.Lanes[0].Events) != 4 || p.Lanes[0].Events[0].Title != "School Dropoff" {
		t.Fatal("calendar lost event history")
	}
}
