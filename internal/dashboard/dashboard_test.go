package dashboard

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "household.sqlite")
	s, e := Open(path, false, "2026-10-13")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
func TestDailyCompletionPersistenceAndIdempotence(t *testing.T) {
	s, path := testStore(t)
	if e := s.Save(Entry{Kind: "chore", Title: "Wash dishes", Owner: "Anyone", Routine: "Evening", Daily: true}); e != nil {
		t.Fatal(e)
	}
	entries, _ := s.List("chore", "2026-10-13")
	id := entries[0].ID
	for i := 0; i < 2; i++ {
		if e := s.SetDone(id, "2026-10-13", true); e != nil {
			t.Fatal(e)
		}
	}
	today, _ := s.Get(id, "2026-10-13")
	tomorrow, _ := s.Get(id, "2026-10-14")
	if !today.Done || tomorrow.Done {
		t.Fatal("daily completion did not stay scoped to its day")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(path, true, "2026-10-14")
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	persisted, _ := reopened.Get(id, "2026-10-13")
	list, _ := reopened.List("chore", "2026-10-14")
	if !persisted.Done || len(list) != 1 || reopened.Demo() {
		t.Fatal("restart lost data or unexpectedly seeded an existing database")
	}
	if e := reopened.Delete(id); e != nil {
		t.Fatal(e)
	}
	var n int
	reopened.db.QueryRow("SELECT COUNT(*) FROM completions").Scan(&n)
	if n != 0 {
		t.Fatal("deletion left orphan completion history")
	}
}
func TestOneTimeChoreAndConsistentBackup(t *testing.T) {
	s, _ := testStore(t)
	s.Save(Entry{Kind: "chore", Title: "Fix bike", Owner: "Anyone", Routine: "Afternoon"})
	entries, _ := s.List("chore", "2026-10-13")
	s.SetDone(entries[0].ID, "2026-10-13", true)
	next, _ := s.Get(entries[0].ID, "2026-10-14")
	if !next.Done {
		t.Fatal("one-time chore unexpectedly reset")
	}
	path := filepath.Join(t.TempDir(), "backup.sqlite")
	if e := s.Backup(path); e != nil {
		t.Fatal(e)
	}
	b, e := Open(path, false, "2026-10-14")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	copy, _ := b.Get(next.ID, "2026-10-14")
	if !copy.Done || copy.Title != "Fix bike" {
		t.Fatal("backup did not contain durable latest state")
	}
	if s.Backup(path) == nil {
		t.Fatal("backup overwrote an existing file")
	}
}
func TestValidationTimeAndDaylightSaving(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	for _, e := range []Entry{
		{Kind: "event", Title: "Event", Date: "2026-10-13", Start: "17:00", End: "16:00"},
		{Kind: "event", Title: "Event", Date: "2026-03-08", Start: "02:30"},
		{Kind: "event", Title: "Event", Date: "2026-02-30"},
		{Kind: "shopping", Title: " "},
	} {
		if Validate(e, loc) == nil {
			t.Fatalf("invalid entry accepted: %#v", e)
		}
	}
	if e := Validate(Entry{Kind: "event", Title: "All day", Date: "2026-10-13"}, loc); e != nil {
		t.Fatal(e)
	}
}
func TestHTTPCreateEscapeCSRFAndCrossOrigin(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	a, e := New(s, loc)
	if e != nil {
		t.Fatal(e)
	}
	first := httptest.NewRecorder()
	a.ServeHTTP(first, httptest.NewRequest("GET", "http://household.test/", nil))
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	cookie := first.Result().Cookies()[0]
	submit := func(origin, token string) *httptest.ResponseRecorder {
		f := url.Values{"csrf": {token}, "title": {"<script>alert(1)</script>"}, "quantity": {"2"}, "return": {"/shopping"}}
		r := httptest.NewRequest("POST", "http://household.test/entries/shopping/0", strings.NewReader(f.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		r.AddCookie(cookie)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if submit("http://attacker.test", cookie.Value).Code != http.StatusForbidden {
		t.Fatal("cross-origin write allowed")
	}
	if submit("http://household.test", "bad").Code != http.StatusForbidden {
		t.Fatal("invalid CSRF accepted")
	}
	good := submit("http://household.test", cookie.Value)
	if good.Code != 200 || !strings.Contains(good.Body.String(), "&lt;script&gt;") || strings.Contains(good.Body.String(), "<script>alert") {
		t.Fatalf("create or escaping failed: %d %s", good.Code, good.Body.String())
	}
	entries, _ := s.List("shopping", "2026-10-13")
	if len(entries) != 1 {
		t.Fatal("blocked requests mutated the database")
	}
	missing := httptest.NewRecorder()
	a.ServeHTTP(missing, httptest.NewRequest("GET", "http://household.test/forms/shopping/999", nil))
	if missing.Code != 404 {
		t.Fatal("missing item form did not return 404")
	}
}
func TestDemoOnlySeedsOnceAndCanBeCleared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.sqlite")
	s, e := Open(path, true, "2026-10-13")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if !s.Demo() {
		t.Fatal("example mode not recorded")
	}
	if e := s.ClearDemo(); e != nil {
		t.Fatal(e)
	}
	if s.Demo() {
		t.Fatal("example mode did not clear")
	}
	events, _ := s.List("event", "2026-10-13")
	if len(events) != 0 {
		t.Fatal("example data remained")
	}
	s.Close()
	s, e = Open(path, true, "2026-10-13")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Demo() {
		t.Fatal("example data reseeded after a deliberate clear")
	}
}

func TestCalendarModesAndOverlapLayout(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	a, e := New(s, loc)
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"day", "3day", "week", "month"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/calendar?mode="+mode+"&date=2026-10-13", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "data-mode=\""+mode+"\"") {
			t.Fatalf("%s: %d %s", mode, w.Code, w.Body.String())
		}
	}
	blocks := layout([]Entry{{ID: 1, Start: "09:00", End: "10:00"}, {ID: 2, Start: "09:15", End: "09:45"}, {ID: 3, Start: "09:20", End: "09:50"}})
	if len(blocks) != 2 || blocks[0].Width != 50 || blocks[1].More != 2 {
		t.Fatalf("overlap grouping failed: %#v", blocks)
	}
	early := layout([]Entry{{Start: "01:00", End: "02:00"}})
	if len(early) != 1 || early[0].Top != 72 {
		t.Fatalf("early event disappeared: %#v", early)
	}
	p := Page{Now: time.Date(2026, 10, 13, 10, 0, 0, 0, loc), Today: "2026-10-13"}
	if e := a.loadCalendar(&p, url.Values{"mode": {"3day"}, "date": {"2026-10-13"}}); e != nil {
		t.Fatal(e)
	}
	if len(p.Lanes) != 3 || p.NextURL != "/calendar?mode=3day&date=2026-10-16" {
		t.Fatalf("three-day navigation failed: %#v", p)
	}
	p = Page{Now: time.Date(2026, 11, 1, 10, 0, 0, 0, loc), Today: "2026-11-01"}
	a.loadCalendar(&p, url.Values{"mode": {"month"}, "date": {"2026-11-01"}})
	if len(p.Cells) != 42 || p.Cells[0].Date != "2026-10-26" || p.NextURL != "/calendar?mode=month&date=2026-12-01" {
		t.Fatal("month calendar alignment failed")
	}
}
