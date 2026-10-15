package dashboard

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func notePeople(t *testing.T, s *Store) (Person, Person) {
	t.Helper()
	if err := s.SavePerson(Person{Name: "Alex"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePerson(Person{Name: "Sam"}); err != nil {
		t.Fatal(err)
	}
	people, err := s.People()
	if err != nil {
		t.Fatal(err)
	}
	return people[0], people[1]
}
func TestNotesAttributionOrderingArchiveAndPersistence(t *testing.T) {
	s, path := testStore(t)
	alex, sam := notePeople(t, s)
	now := time.Now()
	id, err := s.SaveNote(Note{Title: "  A message  ", Body: " One\r\nTwo ", Color: "sage", RecipientID: sam.ID, Pinned: true, CreateKey: "create-once"}, alex, now)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.SaveNote(Note{Title: "A message", Body: "One\nTwo", Color: "sage", CreateKey: "create-once"}, alex, now)
	if err != nil || duplicate != id {
		t.Fatal("duplicate create was not idempotent", duplicate, err)
	}
	later, err := s.SaveNote(Note{Title: "Newer", Body: "Household message", Color: "butter"}, Person{}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Notes(false, 1)
	if err != nil || len(list) != 2 || list[0].ID != id || list[1].ID != later {
		t.Fatal("pinned ordering", list, err)
	}
	n, err := s.Note(id)
	if err != nil || n.AuthorID != alex.ID || n.Author != "Alex" || n.Recipient != "Sam" || n.Title != "A message" || n.Body != "One\nTwo" || n.Version != 1 {
		t.Fatal(n, err)
	}
	decorateNote(&n, time.UTC)
	if n.CreatedLabel == "" || n.UpdatedLabel == "" {
		t.Fatal("new-note dates missing")
	}
	n.Title = "Edited together"
	if _, err = s.SaveNote(n, sam, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveNote(n, alex, now.Add(3*time.Minute)); !errors.Is(err, ErrNoteConflict) {
		t.Fatal("stale edit overwrote content", err)
	}
	edited, _ := s.Note(id)
	if edited.AuthorID != alex.ID || edited.Author != "Alex" || edited.Title != "Edited together" || edited.CreatedAt != n.CreatedAt || edited.Version != 2 {
		t.Fatal("creator or edit state changed", edited)
	}
	if err = s.SetNoteState(id, n.Version, "archived", true, sam, now); !errors.Is(err, ErrNoteConflict) {
		t.Fatal("stale archive accepted", err)
	}
	if err = s.SetNoteState(id, edited.Version, "archived", true, sam, now); err != nil {
		t.Fatal(err)
	}
	board, archive, err := s.NoteCounts()
	if err != nil || board != 1 || archive != 1 {
		t.Fatal(board, archive, err)
	}
	archived, _ := s.Note(id)
	if !archived.Pinned || !archived.Archived {
		t.Fatal("archive discarded pin")
	}
	if _, err = s.SaveNote(archived, alex, now); err == nil {
		t.Fatal("edited archived note")
	}
	if err = s.SetNoteState(id, archived.Version, "pinned", false, alex, now); err == nil {
		t.Fatal("pinned archived note")
	}
	if err = s.SetNoteState(id, archived.Version, "archived", false, alex, now); err != nil {
		t.Fatal(err)
	}
	if err = s.SavePerson(Person{ID: sam.ID, Name: "Sam renamed"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetPersonActive(sam.ID, false); err != nil {
		t.Fatal(err)
	}
	restored, _ := s.Note(id)
	restored.Body = "Keep the archived recipient when editing"
	if _, err = s.SaveNote(restored, alex, now.Add(4*time.Minute)); err != nil {
		t.Fatal("archived existing recipient blocked edit", err)
	}
	n, _ = s.Note(id)
	if n.Recipient != "Sam renamed" || !n.Pinned {
		t.Fatal("rename/restore lost assignment", n)
	}
	if _, err = s.SaveNote(Note{Title: "Bad recipient", Body: "No", Color: "sky", RecipientID: sam.ID}, alex, now); err == nil {
		t.Fatal("new archived recipient accepted")
	}
	if _, err = s.SaveNote(Note{Title: "Bad author", Body: "No", Color: "sky"}, sam, now); err == nil {
		t.Fatal("archived actor accepted")
	}
	if err = s.Save(Entry{Kind: "shopping", Title: "Existing groceries"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path, false, "2026-10-14")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err = s.Note(id)
	if err != nil || n.AuthorID != alex.ID || n.Body != "Keep the archived recipient when editing" {
		t.Fatal("reopen lost notes", n, err)
	}
	entries, err := s.List("shopping", "2026-10-14")
	if err != nil || len(entries) != 1 {
		t.Fatal("notes migration touched entries", entries, err)
	}
}
func TestNoteValidationAndPagination(t *testing.T) {
	s, _ := testStore(t)
	now := time.Now()
	base := Note{Title: "Valid", Body: "Message", Color: "lilac"}
	for _, change := range []func(*Note){func(n *Note) { n.Title = " " }, func(n *Note) { n.Body = " " }, func(n *Note) { n.Title = strings.Repeat("x", 81) }, func(n *Note) { n.Body = strings.Repeat("🙂", 4001) }, func(n *Note) { n.Color = `red; background:url(http://bad.test)` }, func(n *Note) { n.Body = "bad\x00text" }, func(n *Note) { n.RecipientID = -1 }, func(n *Note) { n.RecipientID = 9999 }} {
		n := base
		change(&n)
		if _, err := s.SaveNote(n, Person{}, now); err == nil {
			t.Fatal("invalid note accepted", n.Title)
		}
	}
	for i := 0; i < notesPerPage+1; i++ {
		n := base
		n.Title = fmt.Sprintf("Note %02d", i)
		if _, err := s.SaveNote(n, Person{}, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.Notes(false, 1)
	if err != nil || len(first) != notesPerPage || first[0].Title != "Note 30" {
		t.Fatal(first, err)
	}
	second, err := s.Notes(false, 2)
	if err != nil || len(second) != 1 || second[0].Title != "Note 00" {
		t.Fatal(second, err)
	}
	if _, err = s.Notes(false, 0); err == nil {
		t.Fatal("invalid page accepted")
	}
}
func TestNotesHTTPActorCSRFEditingAndEscaping(t *testing.T) {
	s, _ := testStore(t)
	alex, sam := notePeople(t, s)
	a, err := New(s, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	csrf := strings.Repeat("c", 64)
	request := func(method, path string, values url.Values, actor int64, hx bool) *httptest.ResponseRecorder {
		body := ""
		if values != nil {
			body = values.Encode()
		}
		r := httptest.NewRequest(method, "http://house.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "hq_csrf", Value: csrf})
		r.AddCookie(&http.Cookie{Name: currentPersonCookie, Value: strconv.FormatInt(actor, 10)})
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	form := request("GET", "/notes/new/edit", nil, alex.ID, true)
	if form.Code != 200 || !strings.Contains(form.Body.String(), "From <strong>Alex</strong>") {
		t.Fatal("author default missing", form.Code, form.Body.String())
	}
	key := regexp.MustCompile(`name="create_key" value="([a-f0-9]{48})"`).FindStringSubmatch(form.Body.String())
	if len(key) != 2 {
		t.Fatal("create key missing")
	}
	values := url.Values{"csrf": {csrf}, "actor_id": {strconv.FormatInt(alex.ID, 10)}, "create_key": {key[1]}, "author_id": {strconv.FormatInt(sam.ID, 10)}, "title": {`Hello <script>alert(1)</script>`}, "body": {strings.Repeat("🙂", 4000)}, "color": {"sky"}, "recipient_id": {strconv.FormatInt(sam.ID, 10)}, "return": {"https://evil.test/"}}
	w := request("POST", "/notes/new", values, alex.ID, true)
	if w.Code != 200 || w.Header().Get("HX-Push-Url") != "/notes" || strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("save/escaping/body limit failed", w.Code, w.Body.String())
	}
	list, _ := s.Notes(false, 1)
	if len(list) != 1 || list[0].AuthorID != alex.ID || list[0].RecipientID != sam.ID {
		t.Fatal(list)
	}
	id := list[0].ID
	path := "/notes/" + strconv.FormatInt(id, 10)
	detail := request("GET", path, nil, sam.ID, false)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), strings.Repeat("🙂", 4000)) || !strings.Contains(detail.Body.String(), "From Alex") {
		t.Fatal("reader did not retain full content")
	}
	w = request("POST", "/notes/new", values, sam.ID, true)
	if !strings.Contains(w.Body.String(), "Current person changed") || w.Header().Get("HX-Retarget") != "#drawer" {
		t.Fatal("stale author accepted", w.Code)
	}
	count, _, _ := s.NoteCounts()
	if count != 1 {
		t.Fatal("stale author created a note")
	}
	oversized := url.Values{"csrf": {csrf}, "body": {strings.Repeat("x", 65<<10)}}
	if request("POST", "/notes/new", oversized, alex.ID, true).Code != http.StatusBadRequest {
		t.Fatal("oversized form accepted")
	}
	bad := url.Values{"csrf": {"wrong"}, "title": {"No"}, "body": {"No"}, "color": {"sage"}}
	if request("POST", "/notes/new", bad, alex.ID, true).Code != 403 {
		t.Fatal("CSRF guard missing")
	}
	edited := url.Values{"csrf": {csrf}, "actor_id": {strconv.FormatInt(sam.ID, 10)}, "version": {"1"}, "title": {"Changed"}, "body": {"Second device edit"}, "color": {"peach"}, "recipient_id": {"0"}, "return": {path}}
	w = request("POST", path, edited, sam.ID, true)
	if w.Code != 200 || w.Header().Get("HX-Push-Url") != path {
		t.Fatal("reader edit failed", w.Code, w.Body.String())
	}
	edited.Set("body", "Stale draft must survive")
	w = request("POST", path, edited, sam.ID, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Stale draft must survive") || !strings.Contains(w.Body.String(), "changed on another device") || w.Header().Get("HX-Retarget") != "#drawer" {
		t.Fatal("conflict lost draft", w.Code, w.Body.String())
	}
	stored, _ := s.Note(id)
	if stored.Body != "Second device edit" || stored.AuthorID != alex.ID {
		t.Fatal("conflict overwrote data", stored)
	}
	state := url.Values{"csrf": {csrf}, "actor_id": {strconv.FormatInt(sam.ID, 10)}, "version": {"2"}, "value": {"1"}, "return": {path}}
	w = request("POST", path+"/archived", state, sam.ID, true)
	if w.Code != 200 || w.Header().Get("HX-Push-Url") != "/notes" {
		t.Fatal("archive left dead reader", w.Code, w.Body.String())
	}
	archive := request("GET", "/notes?view=archived", nil, sam.ID, false)
	if archive.Code != 200 || !strings.Contains(archive.Body.String(), ">Changed</h2>") {
		t.Fatal("archive missing note")
	}
	for _, target := range []string{"/notes", "/notes?view=archived&page=2", path, "/weather"} {
		if safeProfileReturn(target) != target {
			t.Fatal("profile picker loses section", target)
		}
	}
	for _, target := range []string{"https://evil.test/notes", "//evil.test/notes", "/notes/../settings", "/notes/4/edit", "javascript:alert(1)"} {
		if safeNoteReturn(target) != "/notes" {
			t.Fatal("unsafe return", target)
		}
	}
	// Normal browser POSTs use PRG rather than leaving a resubmittable form.
	state.Set("version", "3")
	state.Set("value", "0")
	w = request("POST", path+"/archived", state, sam.ID, false)
	if w.Code != 303 || w.Header().Get("Location") != "/notes?view=archived" {
		t.Fatal("restore redirect", w.Code, w.Header())
	}
}
