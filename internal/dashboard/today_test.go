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
	if total != 4 || len(groups) != 3 || groups[0].Name != "Anyone" || groups[0].Count != 2 || groups[0].URL != "/chores?person=none" || groups[1].Name != "Alex" || groups[1].URL != "/chores?person=8" {
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
	if !strings.Contains(body, "Alex: 1 pending chore") || !strings.Contains(body, "Anyone: 1 pending chore") || !strings.Contains(body, "Any store: 1 shopping item needed") || strings.Contains(body, "Complete Alex task") {
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
