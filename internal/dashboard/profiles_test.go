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

func TestProfileDefaultsAreLocalAndCalendarPreferenceFollowsPerson(t *testing.T) {
	s, _ := testStore(t)
	s.SavePerson(Person{Name: "Alex"})
	s.SavePerson(Person{Name: "Sam"})
	people, _ := s.People()
	alex, sam := people[0], people[1]
	a, e := New(s, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	get := func(path string, id int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://house.test"+path, nil)
		if id > 0 {
			r.AddCookie(&http.Cookie{Name: currentPersonCookie, Value: strconv.FormatInt(id, 10)})
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	body := get("/forms/chore/new", alex.ID).Body.String()
	if !strings.Contains(body, `value="`+strconv.FormatInt(alex.ID, 10)+`" selected`) || !strings.Contains(body, `name="actor_id" value="`+strconv.FormatInt(alex.ID, 10)+`"`) {
		t.Fatal("current person not defaulted", body)
	}
	if strings.Contains(get("/forms/chore/new", 0).Body.String(), `name="actor_id" value="`+strconv.FormatInt(alex.ID, 10)+`"`) {
		t.Fatal("profile leaked between browsers")
	}
	if get("/calendar?mode=week", alex.ID).Code != 200 {
		t.Fatal("calendar request")
	}
	mode, _ := s.personCalendarMode(alex.ID)
	if mode != "week" {
		t.Fatal("calendar preference not saved")
	}
	get("/calendar?mode=month", sam.ID)
	get("/calendar", alex.ID)
	mode, _ = s.personCalendarMode(alex.ID)
	if mode != "week" {
		t.Fatal("borrowed other person's view")
	}
	csrf := strings.Repeat("c", 64)
	values := url.Values{"csrf": {csrf}, "person_id": {strconv.FormatInt(alex.ID, 10)}, "return": {"/media"}}
	r := httptest.NewRequest("POST", "http://house.test/current-person", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r.AddCookie(&http.Cookie{Name: "hq_csrf", Value: csrf})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("HX-Redirect") != "/media" {
		t.Fatal(w.Code, w.Body.String())
	}
	c := w.Result().Cookies()[0]
	if c.Name != currentPersonCookie || !c.HttpOnly || c.MaxAge != 31536000 {
		t.Fatal("invalid remembered profile cookie")
	}
	s.SetPersonActive(alex.ID, false)
	if a.currentPerson(r).ID != 0 || get("/forms/event/new", alex.ID).Code != 200 {
		t.Fatal("archive profile fallback")
	}
	for _, bad := range []string{"https://evil.test/", "//evil.test/media", "/unknown"} {
		if safeProfileReturn(bad) != "/" {
			t.Fatal("unsafe profile redirect")
		}
	}
}
func TestEntryAttributionCreditsCompleterAndKeepsCreator(t *testing.T) {
	s, _ := testStore(t)
	s.SavePerson(Person{Name: "Alex"})
	s.SavePerson(Person{Name: "Sam"})
	people, _ := s.People()
	alex, sam := people[0], people[1]
	now := time.Now()
	e := Entry{Kind: "chore", Title: "Dishes", PersonID: alex.ID, Owner: alex.Name, Routine: "Evening", Daily: true}
	if err := s.SaveAs(e, alex, now); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List("chore", now.Format("2006-01-02"))
	e = list[0]
	if e.CreatedByID != alex.ID || e.CreatedBy != alex.Name {
		t.Fatal("missing creator")
	}
	e.Title = "Clean dishes"
	if err := s.SaveAs(e, sam, now); err != nil {
		t.Fatal(err)
	}
	e, _ = s.Get(e.ID, now.Format("2006-01-02"))
	if e.CreatedByID != alex.ID {
		t.Fatal("editing overwrote creator")
	}
	day := now.Format("2006-01-02")
	for range 2 {
		if err := s.SetDoneAs(e.ID, day, true, now, sam); err != nil {
			t.Fatal(err)
		}
	}
	var credited, assigned int64
	var name string
	if err := s.db.QueryRow("SELECT person_id,person_name,assigned_person_id FROM chore_history").Scan(&credited, &name, &assigned); err != nil {
		t.Fatal(err)
	}
	if credited != sam.ID || assigned != alex.ID || name != sam.Name {
		t.Fatal("assignee confused with completer")
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM entry_activity WHERE action='completed'").Scan(&count)
	if count != 1 {
		t.Fatal("repeated completion duplicated activity")
	}
	s.SetDoneAs(e.ID, day, false, now, sam)
	s.db.QueryRow("SELECT count(*) FROM chore_history").Scan(&count)
	if count != 0 {
		t.Fatal("undo kept chore points")
	}
	s.SetDoneAs(e.ID, day, true, now, Person{})
	var anonymous bool
	s.db.QueryRow("SELECT person_id IS NULL FROM chore_history").Scan(&anonymous)
	if !anonymous {
		t.Fatal("household completion invented personal credit")
	}
	if err := s.SaveAs(Entry{Kind: "shopping", Title: "Milk"}, Person{ID: 999}, now); err == nil {
		t.Fatal("unknown creator accepted")
	}
	shopping, _ := s.List("shopping", day)
	if len(shopping) != 0 {
		t.Fatal("failed attribution left item behind")
	}
	s.Delete(e.ID)
	s.db.QueryRow("SELECT count(*) FROM entry_activity WHERE entry_id=?", e.ID).Scan(&count)
	if count < 4 {
		t.Fatal("deletion erased activity")
	}
}
