package dashboard

import (
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestShoppingStoreAssignmentRenameArchiveAndRestart(t *testing.T) {
	s, path := testStore(t)
	shops, err := s.Shops()
	if err != nil || len(shops) != 3 {
		t.Fatal("default stores missing", err, shops)
	}
	target := shops[1]
	if target.Name != "Target" {
		t.Fatal("unexpected defaults")
	}
	if err = s.Save(Entry{Kind: "shopping", Title: "Soap", Quantity: "2", ShopID: target.ID}); err != nil {
		t.Fatal(err)
	}
	entries, _ := s.List("shopping", "2026-10-13")
	id := entries[0].ID
	target.Name = "Target downtown"
	if err = s.SaveShop(target); err != nil {
		t.Fatal(err)
	}
	entry, _ := s.Get(id, "2026-10-13")
	if entry.ShopID != target.ID || entry.ShopName != "Target downtown" {
		t.Fatal("rename broke item assignment")
	}
	if err = s.SaveShop(Shop{Name: "TARGET DOWNTOWN"}); err == nil {
		t.Fatal("duplicate store accepted")
	}
	s.SetShopActive(target.ID, false)
	s.Close()
	s, err = Open(path, false, "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	shops, _ = s.Shops()
	if len(shops) != 3 {
		t.Fatal("restart reseeded store names")
	}
	target, _ = s.Shop(target.ID)
	if target.Active || target.Name != "Target downtown" {
		t.Fatal("archive/rename did not persist")
	}
	entry, _ = s.Get(id, "2026-10-13")
	if entry.ShopName != target.Name {
		t.Fatal("archiving lost assigned store")
	}
	s.SetShopActive(target.ID, true)
	target, _ = s.Shop(target.ID)
	if !target.Active {
		t.Fatal("store not restored")
	}
}
func TestStoreFilteredClearAndUnknownFilter(t *testing.T) {
	s, _ := testStore(t)
	shops, _ := s.Shops()
	for _, e := range []Entry{{Kind: "shopping", Title: "Target soap", ShopID: shops[1].ID}, {Kind: "shopping", Title: "Costco milk", ShopID: shops[2].ID}, {Kind: "shopping", Title: "Anywhere apples"}, {Kind: "shopping", Title: "Target bread", ShopID: shops[1].ID}} {
		if err := s.Save(e); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := s.List("shopping", "2026-10-13")
	for _, e := range entries[:3] {
		s.SetDone(e.ID, "2026-10-13", true)
	}
	if err := s.ClearShoppingFilter(strconv.FormatInt(shops[1].ID, 10)); err != nil {
		t.Fatal(err)
	}
	entries, _ = s.List("shopping", "2026-10-13")
	if len(entries) != 3 {
		t.Fatal("clear removed items outside selected store", entries)
	}
	for _, e := range entries {
		if e.Title == "Target soap" {
			t.Fatal("selected picked-up item was not cleared")
		}
	}
	if err := s.ClearShoppingFilter("none"); err != nil {
		t.Fatal(err)
	}
	entries, _ = s.List("shopping", "2026-10-13")
	if len(entries) != 2 || entries[0].Title != "Costco milk" || entries[1].Title != "Target bread" {
		t.Fatal("unassigned clear affected other stores", entries)
	}
	if _, err := shopFilter("999", shops); err == nil {
		t.Fatal("unknown store filter accepted")
	}
	if err := s.ClearShoppingFilter("invalid"); err == nil {
		t.Fatal("malformed filter accepted")
	}
}
func TestShoppingStoresHTTPFilteringAndValidation(t *testing.T) {
	s, _ := testStore(t)
	shops, _ := s.Shops()
	target := shops[1]
	costco := shops[2]
	loc, _ := time.LoadLocation("America/New_York")
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test"+path, nil))
		return w
	}
	first := get("/settings?section=stores")
	if first.Code != 200 || !strings.Contains(first.Body.String(), "Stop &amp; Shop") {
		t.Fatal("store settings missing", first.Body.String())
	}
	cookie := first.Result().Cookies()[0]
	post := func(path string, values url.Values) *httptest.ResponseRecorder {
		values.Set("csrf", cookie.Value)
		r := httptest.NewRequest("POST", "http://household.test"+path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		r.Header.Set("HX-Current-URL", "http://household.test/shopping?store="+strconv.FormatInt(target.ID, 10))
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	create := func(name string, id int64) *httptest.ResponseRecorder {
		return post("/entries/shopping/0", url.Values{"title": {name}, "store_id": {strconv.FormatInt(id, 10)}, "return": {"/shopping"}})
	}
	create("Target soap", target.ID)
	create("Costco milk", costco.ID)
	create("Anywhere apples", 0)
	targetURL := "/shopping?store=" + strconv.FormatInt(target.ID, 10)
	body := get(targetURL).Body.String()
	if !strings.Contains(body, "Target soap") || strings.Contains(body, "Costco milk") || strings.Contains(body, "Anywhere apples") {
		t.Fatal("store tab leaked other items")
	}
	body = get("/shopping?store=none").Body.String()
	if !strings.Contains(body, "Anywhere apples") || strings.Contains(body, "Target soap") {
		t.Fatal("Any store filter failed")
	}
	form := get("/forms/shopping/new?return=" + url.QueryEscape(targetURL)).Body.String()
	if !strings.Contains(form, `value="`+strconv.FormatInt(target.ID, 10)+`" selected`) {
		t.Fatal("new item did not default to selected store", form)
	}
	entries, _ := s.List("shopping", "2026-10-13")
	id := entries[0].ID
	changed := post("/entries/shopping/"+strconv.FormatInt(id, 10), url.Values{"title": {"Target soap"}, "store_id": {strconv.FormatInt(costco.ID, 10)}, "return": {targetURL}})
	if changed.Header().Get("HX-Push-Url") != "/shopping?store="+strconv.FormatInt(costco.ID, 10) {
		t.Fatal("changed assignment left saved item hidden", changed.Header())
	}
	s.SetShopActive(costco.ID, false)
	if w := create("Bad archived", costco.ID); !strings.Contains(w.Body.String(), "Choose an active store") {
		t.Fatal("archived store accepted for new item")
	}
	edit := get("/forms/shopping/" + strconv.FormatInt(id, 10)).Body.String()
	if !strings.Contains(edit, "Costco (archived)") {
		t.Fatal("existing archived assignment not selectable")
	}
	if w := create("Unknown", 999); !strings.Contains(w.Body.String(), "no longer available") {
		t.Fatal("unknown store accepted")
	}
	for _, entry := range entries {
		s.SetDone(entry.ID, "2026-10-13", true)
	}
	if w := post("/shopping/clear", url.Values{"return": {"/shopping?store=999"}}); w.Code != 400 {
		t.Fatal("invalid clear filter did not fail safely")
	}
	entries, _ = s.List("shopping", "2026-10-13")
	if len(entries) != 3 {
		t.Fatal("invalid filtered clear deleted data")
	}
}
