package dashboard

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func noteCreateOwner(csrf string) string {
	sum := sha256.Sum256([]byte(csrf))
	return fmt.Sprintf("%x", sum)
}

func notesBoardURL(archived bool, page int) string {
	q := url.Values{}
	if archived {
		q.Set("view", "archived")
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) > 0 {
		return "/notes?" + q.Encode()
	}
	return "/notes"
}
func noteURL(n Note) string { return "/notes/" + strconv.FormatInt(n.ID, 10) }
func safeNoteReturn(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return "/notes"
	}
	if u.Path == "/notes" {
		page, _ := strconv.Atoi(u.Query().Get("page"))
		if page < 1 || page > 1000000 {
			page = 1
		}
		return notesBoardURL(u.Query().Get("view") == "archived", page)
	}
	if strings.HasPrefix(u.Path, "/notes/") {
		id, err := strconv.ParseInt(strings.TrimPrefix(u.Path, "/notes/"), 10, 64)
		if err == nil && id > 0 {
			return "/notes/" + strconv.FormatInt(id, 10)
		}
	}
	return "/notes"
}
func decorateNote(n *Note, zone *time.Location) {
	format := func(value string) string {
		at, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return "Date unavailable"
		}
		local := at.In(zone)
		now := time.Now().In(zone)
		if local.Format("2006-01-02") == now.Format("2006-01-02") {
			return "Today, " + local.Format("3:04 PM")
		}
		if local.Year() == now.Year() {
			return local.Format("Jan 2, 3:04 PM")
		}
		return local.Format("Jan 2, 2006")
	}
	n.CreatedLabel, n.UpdatedLabel = format(n.CreatedAt), format(n.UpdatedAt)
}
func (a *App) loadNotes(p *Page, q url.Values) error {
	var err error
	p.Title = "Notes"
	p.NoteArchived = q.Get("view") == "archived"
	p.NoteCount, p.NoteArchiveCount, err = a.store.NoteCounts()
	if err != nil {
		return err
	}
	count := p.NoteCount
	if p.NoteArchived {
		count = p.NoteArchiveCount
	}
	p.NotePages = max(1, (count+notesPerPage-1)/notesPerPage)
	p.NotePage, _ = strconv.Atoi(q.Get("page"))
	p.NotePage = min(max(1, p.NotePage), p.NotePages)
	p.Notes, err = a.store.Notes(p.NoteArchived, p.NotePage)
	if err != nil {
		return err
	}
	for i := range p.Notes {
		decorateNote(&p.Notes[i], a.loc)
	}
	p.Return = notesBoardURL(p.NoteArchived, p.NotePage)
	p.NotePrev = notesBoardURL(p.NoteArchived, p.NotePage-1)
	p.NoteNext = notesBoardURL(p.NoteArchived, p.NotePage+1)
	return nil
}
func (a *App) registerNotes(mux *http.ServeMux) {
	mux.HandleFunc("GET /notes/{id}", a.noteDetail)
	mux.HandleFunc("GET /notes/{id}/edit", a.noteForm)
	mux.HandleFunc("POST /notes/{id}", a.saveNote)
	mux.HandleFunc("POST /notes/{id}/{action}", a.noteState)
}
func (a *App) noteDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	p := a.base(w, r)
	p.View = "note"
	p.Return = "/notes/" + strconv.FormatInt(id, 10)
	p.Note, err = a.store.Note(id)
	if err != nil {
		a.fail(w, err)
		return
	}
	decorateNote(&p.Note, a.loc)
	if err = a.load(&p, r.URL.Query()); err != nil {
		a.fail(w, err)
		return
	}
	p.Title = "Notes"
	p.NoteArchived = p.Note.Archived
	name := "page"
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
		name = "fragment"
		w.Header().Set("HX-Push-Url", p.Return)
	}
	a.render(w, name, p)
}
func (a *App) noteForm(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = safeNoteReturn(r.URL.Query().Get("return"))
	p.NoteColors = noteColors
	p.FormTitle = "New note"
	p.Note = Note{Color: "butter", Author: p.CurrentPerson.Name, AuthorID: p.CurrentPerson.ID, CreateKey: randomToken()}
	if p.Note.Author == "" {
		p.Note.Author = "Household"
	}
	if r.PathValue("id") != "new" {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		p.Note, err = a.store.Note(id)
		if err != nil {
			a.fail(w, err)
			return
		}
		if p.Note.Archived {
			http.Error(w, "Restore this note before editing it.", http.StatusConflict)
			return
		}
		p.FormTitle = "Edit note"
	}
	var err error
	p.People, err = a.store.People()
	if err != nil {
		a.fail(w, err)
		return
	}
	a.render(w, "note-form", p)
}
func noteUserError(err error) bool {
	var input noteInputError
	return errors.As(err, &input) || errors.Is(err, ErrNoteConflict)
}
func (a *App) saveNote(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = safeNoteReturn(r.Form.Get("return"))
	p.NoteColors = noteColors
	p.FormTitle = "New note"
	var err error
	if r.PathValue("id") != "new" {
		p.Note.ID, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || p.Note.ID <= 0 {
			http.NotFound(w, r)
			return
		}
		p.FormTitle = "Edit note"
	}
	p.Note.Title, p.Note.Body, p.Note.Color = r.Form.Get("title"), r.Form.Get("body"), r.Form.Get("color")
	p.Note.Pinned = r.Form.Get("pinned") == "on"
	p.Note.CreateKey = r.Form.Get("create_key")
	p.Note.Version, _ = strconv.Atoi(r.Form.Get("version"))
	actor, err := a.entryActor(r)
	if err != nil {
		err = noteInputError(err.Error())
	}
	if err == nil {
		rawRecipient := r.Form.Get("recipient_id")
		if rawRecipient == "" {
			rawRecipient = "0"
		}
		p.Note.RecipientID, err = strconv.ParseInt(rawRecipient, 10, 64)
		if err != nil {
			err = noteInputError("Choose a recipient from the list.")
		}
	}
	if err == nil && p.Note.ID == 0 && (len(p.Note.CreateKey) != 48 || strings.Trim(p.Note.CreateKey, "0123456789abcdef") != "") {
		err = noteInputError("Reload the new-note form and try again.")
	}
	inputKey := p.Note.CreateKey
	if err == nil && p.Note.ID == 0 {
		p.Note.CreateKey = noteCreateOwner(p.CSRF) + ":" + p.Note.CreateKey
	}
	if err == nil {
		_, err = a.store.SaveNote(p.Note, actor, time.Now())
	}
	if err != nil {
		if !noteUserError(err) {
			a.fail(w, err)
			return
		}
		p.Note.CreateKey = inputKey
		p.Error = err.Error()
		p.Note.Author = p.CurrentPerson.Name
		if p.Note.Author == "" {
			p.Note.Author = "Household"
		}
		if p.Note.ID > 0 {
			old, e := a.store.Note(p.Note.ID)
			if e != nil {
				a.fail(w, e)
				return
			}
			p.Note.Author = old.Author
		}
		p.People, err = a.store.People()
		if err != nil {
			a.fail(w, err)
			return
		}
		w.Header().Set("HX-Retarget", "#drawer")
		w.Header().Set("HX-Reswap", "innerHTML")
		a.render(w, "note-form", p)
		return
	}
	a.noteResponse(w, r, p.Return, "Note saved.", "")
}
func (a *App) noteResponse(w http.ResponseWriter, r *http.Request, target, notice, message string) {
	target = safeNoteReturn(target)
	if r.Header.Get("HX-Request") != "true" && message == "" {
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	p := a.base(w, r)
	p.Return = target
	p.View = "notes"
	p.Notice = notice
	p.Error = message
	u, _ := url.Parse(target)
	if u.Path != "/notes" {
		id, _ := strconv.ParseInt(strings.TrimPrefix(u.Path, "/notes/"), 10, 64)
		var err error
		p.Note, err = a.store.Note(id)
		if err != nil {
			a.fail(w, err)
			return
		}
		decorateNote(&p.Note, a.loc)
		p.View = "note"
		p.NoteArchived = p.Note.Archived
	}
	if err := a.load(&p, u.Query()); err != nil {
		a.fail(w, err)
		return
	}
	p.Title = "Notes"
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Retarget", "#content")
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("HX-Push-Url", p.Return)
		a.render(w, "fragment", p)
	} else {
		a.render(w, "page", p)
	}
}
func (a *App) noteState(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	action := r.PathValue("action")
	if action != "pinned" && action != "archived" {
		http.NotFound(w, r)
		return
	}
	raw := r.Form.Get("value")
	if raw != "0" && raw != "1" {
		http.Error(w, "Invalid note action", http.StatusBadRequest)
		return
	}
	old, err := a.store.Note(id)
	if err != nil {
		a.fail(w, err)
		return
	}
	version, _ := strconv.Atoi(r.Form.Get("version"))
	actor, err := a.entryActor(r)
	if err != nil {
		err = noteInputError(err.Error())
	}
	if err == nil {
		err = a.store.SetNoteState(id, version, action, raw == "1", actor, time.Now())
	}
	target := safeNoteReturn(r.Form.Get("return"))
	if err != nil {
		if !noteUserError(err) {
			a.fail(w, err)
			return
		}
		a.noteResponse(w, r, target, "", err.Error())
		return
	}
	notice := "Note unpinned."
	if action == "pinned" && raw == "1" {
		notice = "Note pinned."
	}
	if action == "archived" {
		notice = "Note restored."
		if raw == "1" {
			notice = "Note archived."
		}
		if strings.HasPrefix(target, "/notes/") {
			target = notesBoardURL(old.Archived, 1)
		}
	}
	a.noteResponse(w, r, target, notice, "")
}
