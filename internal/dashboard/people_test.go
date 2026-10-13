package dashboard

import (
	"bytes"
	"database/sql"
	"image"
	imgcolor "image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPeopleMigrationAndRenameKeepsAssignment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE entries(id INTEGER PRIMARY KEY,kind TEXT NOT NULL,title TEXT NOT NULL,owner TEXT NOT NULL DEFAULT 'Anyone',routine TEXT NOT NULL DEFAULT 'Morning',quantity TEXT NOT NULL DEFAULT '',date TEXT NOT NULL DEFAULT '',start TEXT NOT NULL DEFAULT '',end TEXT NOT NULL DEFAULT '',daily INTEGER NOT NULL DEFAULT 0,done INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
		`INSERT INTO settings VALUES('initialized','1'),('demo','false')`,
		`INSERT INTO entries(id,kind,title,owner) VALUES(1,'chore','Feed cat','Alex'),(2,'event','Practice','alex'),(3,'chore','Dishes','Anyone')`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := Open(path, false, "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	people, err := s.People()
	if err != nil || len(people) != 1 {
		t.Fatalf("migration: %v %#v", err, people)
	}
	person := people[0]
	chore, _ := s.Get(1, "2026-10-13")
	event, _ := s.Get(2, "2026-10-13")
	if chore.PersonID != person.ID || event.PersonID != person.ID {
		t.Fatal("legacy assignments were not linked")
	}
	person.Name = "Alexandra"
	if err = s.SavePerson(person); err != nil {
		t.Fatal(err)
	}
	chore, _ = s.Get(1, "2026-10-13")
	if chore.Owner != "Alexandra" || chore.PersonID != person.ID {
		t.Fatal("rename broke assignment")
	}
	if err = s.SavePerson(Person{Name: "aLEXANDRA"}); err == nil {
		t.Fatal("duplicate name accepted")
	}
	s.SetPersonActive(person.ID, false)
	s.Close()
	s, err = Open(path, false, "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	person, _ = s.Person(person.ID)
	if person.Active || person.Name != "Alexandra" {
		t.Fatal("restart changed archived person")
	}
	people, _ = s.People()
	if len(people) != 1 {
		t.Fatal("restart repeated legacy migration")
	}
}
func TestChoreCreditPersistsThroughRenameArchiveAndDeletion(t *testing.T) {
	s, _ := testStore(t)
	if err := s.SavePerson(Person{Name: "Sam"}); err != nil {
		t.Fatal(err)
	}
	people, _ := s.People()
	sam := people[0]
	if err := s.Save(Entry{Kind: "chore", Title: "Dishes", PersonID: sam.ID, Owner: sam.Name, Routine: "Evening", Daily: true}); err != nil {
		t.Fatal(err)
	}
	entries, _ := s.List("chore", "2026-10-13")
	id := entries[0].ID
	at := time.Date(2026, 10, 14, 0, 30, 0, 0, time.UTC)
	for range 2 {
		if err := s.SetDoneAt(id, "2026-10-13", true, at); err != nil {
			t.Fatal(err)
		}
	}
	sam.Name = "Samuel"
	s.SavePerson(sam)
	s.SetPersonActive(sam.ID, false)
	s.SetDoneAt(id, "2026-10-14", true, at.Add(24*time.Hour))
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM chore_history").Scan(&count)
	if count != 2 {
		t.Fatal("duplicate credit or missing daily credit", count)
	}
	var name, day, stamp string
	var person int64
	s.db.QueryRow("SELECT person_name,person_id,day,completed_at FROM chore_history ORDER BY id LIMIT 1").Scan(&name, &person, &day, &stamp)
	if name != "Sam" || person != sam.ID || day != "2026-10-13" || stamp != at.Format(time.RFC3339Nano) {
		t.Fatal("credit snapshot changed", name, person, day, stamp)
	}
	s.SetDoneAt(id, "2026-10-14", false, at)
	s.db.QueryRow("SELECT COUNT(*) FROM chore_history").Scan(&count)
	if count != 1 {
		t.Fatal("unchecking removed wrong day's credit")
	}
	s.Delete(id)
	s.db.QueryRow("SELECT COUNT(*) FROM chore_history").Scan(&count)
	if count != 1 {
		t.Fatal("deleting chore erased past credit")
	}
	s.Save(Entry{Kind: "chore", Title: "New chore", PersonID: sam.ID, Owner: sam.Name, Routine: "Morning"})
	entries, _ = s.List("chore", "2026-10-13")
	id = entries[0].ID
	s.SetDoneAt(id, "2026-10-13", true, at)
	s.SetDoneAt(id, "2026-10-14", true, at.Add(24*time.Hour))
	s.db.QueryRow("SELECT COUNT(*) FROM chore_history").Scan(&count)
	if count != 2 {
		t.Fatal("one-time credit duplicated or task ID reuse lost credit", count)
	}
	s.SetDoneAt(id, "2026-10-15", false, at)
	s.db.QueryRow("SELECT COUNT(*) FROM chore_history").Scan(&count)
	if count != 1 {
		t.Fatal("one-time uncheck removed past unrelated credit")
	}
}
func TestPeoplePhotoAndAssignmentHTTP(t *testing.T) {
	s, _ := testStore(t)
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
	first := get("/settings")
	if first.Code != 200 || !strings.Contains(first.Body.String(), "Who’s in your household?") {
		t.Fatal(first.Body.String())
	}
	cookie := first.Result().Cookies()[0]
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	img.Set(3, 3, imgcolor.RGBA{255, 100, 50, 255})
	var photo bytes.Buffer
	png.Encode(&photo, img)
	upload := func(token string, photoData []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		mw.WriteField("csrf", token)
		mw.WriteField("name", "Taylor")
		part, _ := mw.CreateFormFile("photo", "avatar.png")
		part.Write(photoData)
		mw.Close()
		r := httptest.NewRequest("POST", "http://household.test/people/0", &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("HX-Request", "true")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if w := upload("bad", photo.Bytes()); w.Code != 403 {
		t.Fatal("multipart CSRF bypass", w.Code)
	}
	if w := upload(cookie.Value, []byte("<svg></svg>")); !strings.Contains(w.Body.String(), "Choose a JPG or PNG photo.") {
		t.Fatal("invalid photo accepted", w.Body.String())
	}
	if w := upload(cookie.Value, photo.Bytes()); w.Code != 200 || !strings.Contains(w.Body.String(), "Taylor") {
		t.Fatal("photo create failed", w.Code, w.Body.String())
	}
	people, _ := s.People()
	if len(people) != 1 || !people[0].HasPhoto {
		t.Fatal("photo not saved")
	}
	person := people[0]
	got := get("/people/" + strconv.FormatInt(person.ID, 10) + "/photo")
	if got.Code != 200 || got.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal("photo not served")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(got.Body.Bytes()))
	if err != nil || format != "jpeg" || cfg.Width != 256 || cfg.Height != 256 {
		t.Fatal("photo not normalized")
	}
	form := get("/forms/chore/new")
	if !strings.Contains(form.Body.String(), `name="person_id"`) || !strings.Contains(form.Body.String(), "Taylor") {
		t.Fatal("dropdown missing", form.Body.String())
	}
	post := func(personID string) *httptest.ResponseRecorder {
		values := url.Values{"csrf": {cookie.Value}, "title": {"Feed cat"}, "routine": {"Morning"}, "person_id": {personID}, "return": {"/chores"}}
		r := httptest.NewRequest("POST", "http://household.test/entries/chore/0", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if w := post("999"); !strings.Contains(w.Body.String(), "no longer available") {
		t.Fatal("unknown person accepted")
	}
	if w := post(strconv.FormatInt(person.ID, 10)); w.Code != 200 || !strings.Contains(w.Body.String(), "Taylor") {
		t.Fatal("assignment failed", w.Body.String())
	}
	s.SetPersonActive(person.ID, false)
	if strings.Contains(get("/forms/chore/new").Body.String(), "<option value=\""+strconv.FormatInt(person.ID, 10)+"\"") {
		t.Fatal("archived person in new assignments")
	}
	if w := post(strconv.FormatInt(person.ID, 10)); !strings.Contains(w.Body.String(), "Choose an active person") {
		t.Fatal("archived new assignment accepted")
	}
	entries, _ := s.List("chore", "2026-10-13")
	if len(entries) != 1 || entries[0].PersonID != person.ID {
		t.Fatal("failed writes mutated chores")
	}
	edit := get("/forms/chore/" + strconv.FormatInt(entries[0].ID, 10))
	if !strings.Contains(edit.Body.String(), "Taylor (archived)") {
		t.Fatal("existing archived assignment lost")
	}
	person.Photo = []byte{}
	if err = s.SavePerson(person); err != nil {
		t.Fatal(err)
	}
	if get("/people/"+strconv.FormatInt(person.ID, 10)+"/photo").Code != http.StatusNotFound {
		t.Fatal("removed photo still served")
	}
	if _, err = avatar(bytes.NewReader(make([]byte, (3<<20)+1))); err == nil {
		t.Fatal("oversized photo accepted")
	}
}
