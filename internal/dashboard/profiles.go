package dashboard

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// A household profile is a convenient identity, not an authentication boundary.
const currentPersonCookie = "hq_person"

func (s *Store) migrateProfiles() error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`CREATE TABLE IF NOT EXISTS person_preferences(person_id INTEGER PRIMARY KEY REFERENCES people(id),calendar_mode TEXT NOT NULL DEFAULT 'day')`); e != nil {
		return e
	}
	if _, e = tx.Exec(`CREATE TABLE IF NOT EXISTS entry_activity(id INTEGER PRIMARY KEY,entry_key TEXT NOT NULL,entry_id INTEGER NOT NULL,kind TEXT NOT NULL,action TEXT NOT NULL,actor_id INTEGER REFERENCES people(id),actor_name TEXT NOT NULL,title TEXT NOT NULL,occurred_at TEXT NOT NULL,cycle TEXT NOT NULL DEFAULT '')`); e != nil {
		return e
	}
	if _, e = tx.Exec(`CREATE INDEX IF NOT EXISTS entry_activity_person_time ON entry_activity(actor_id,occurred_at)`); e != nil {
		return e
	}
	for table, cols := range map[string]map[string]string{
		"entries":       {"created_by": "INTEGER REFERENCES people(id)", "created_by_name": "TEXT NOT NULL DEFAULT ''", "created_at": "TEXT NOT NULL DEFAULT ''", "completed_by": "INTEGER REFERENCES people(id)", "completed_by_name": "TEXT NOT NULL DEFAULT ''", "completed_at": "TEXT NOT NULL DEFAULT ''"},
		"chore_history": {"assigned_person_id": "INTEGER REFERENCES people(id)", "assigned_person_name": "TEXT NOT NULL DEFAULT ''"},
	} {
		rows, e := tx.Query("PRAGMA table_info(" + table + ")")
		if e != nil {
			return e
		}
		existing := map[string]bool{}
		for rows.Next() {
			var cid, nn, pk int
			var name, typ string
			var def any
			if e = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); e != nil {
				rows.Close()
				return e
			}
			existing[name] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for name, definition := range cols {
			if !existing[name] {
				if _, e = tx.Exec("ALTER TABLE " + table + " ADD COLUMN " + name + " " + definition); e != nil {
					return e
				}
			}
		}
	}
	return tx.Commit()
}
func actorInTransaction(tx *sql.Tx, actor Person) (Person, error) {
	if actor.ID == 0 {
		return Person{}, nil
	}
	var active bool
	if actor.ID < 0 {
		return Person{}, errors.New("Choose an active person")
	}
	if e := tx.QueryRow("SELECT name,active FROM people WHERE id=?", actor.ID).Scan(&actor.Name, &active); e != nil || !active {
		return Person{}, errors.New("Choose an active person")
	}
	return actor, nil
}
func recordEntryActivity(tx *sql.Tx, id int64, action string, actor Person, stamp, cycle string) error {
	_, e := tx.Exec(`INSERT INTO entry_activity(entry_key,entry_id,kind,action,actor_id,actor_name,title,occurred_at,cycle) SELECT entry_key,id,kind,?,?,?,title,?,? FROM entries WHERE id=?`, action, personRef(actor.ID), actor.Name, stamp, cycle, id)
	return e
}
func (a *App) currentPerson(r *http.Request) Person {
	c, e := r.Cookie(currentPersonCookie)
	if e != nil {
		return Person{}
	}
	id, e := strconv.ParseInt(c.Value, 10, 64)
	if e != nil || id <= 0 {
		return Person{}
	}
	p, e := a.store.Person(id)
	if e != nil || !p.Active {
		return Person{}
	}
	return p
}
func (a *App) entryActor(r *http.Request) (Person, error) {
	actor := a.currentPerson(r)
	if r.Form.Has("actor_id") {
		id, e := strconv.ParseInt(r.Form.Get("actor_id"), 10, 64)
		if e != nil || id != actor.ID {
			return Person{}, errors.New("Current person changed. Reload this form before saving")
		}
	}
	return actor, nil
}
func safeProfileReturn(value string) string {
	u, e := url.Parse(value)
	if e != nil || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return "/"
	}
	switch u.Path {
	case "/", "/calendar", "/chores", "/shopping", "/sports", "/media", "/settings", "/cookbook":
		return u.RequestURI()
	}
	return "/"
}
func (a *App) currentPersonForm(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.People, _ = a.store.People()
	p.Return = safeProfileReturn(r.Header.Get("HX-Current-URL"))
	if u, e := url.Parse(r.Header.Get("HX-Current-URL")); e == nil {
		p.Return = safeProfileReturn(u.RequestURI())
	}
	a.render(w, "current-person-form", p)
}
func (a *App) setCurrentPerson(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(r.Form.Get("person_id"), 10, 64)
	if e != nil || id < 0 {
		http.Error(w, "Choose a person", 400)
		return
	}
	if id > 0 {
		p, e := a.store.Person(id)
		if e != nil || !p.Active {
			http.Error(w, "Choose an active person", 400)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: currentPersonCookie, Value: strconv.FormatInt(id, 10), Path: "/", MaxAge: 31536000, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", safeProfileReturn(r.Form.Get("return")))
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, safeProfileReturn(r.Form.Get("return")), http.StatusSeeOther)
}
func (s *Store) personCalendarMode(id int64) (string, error) {
	var mode string
	e := s.db.QueryRow("SELECT calendar_mode FROM person_preferences WHERE person_id=?", id).Scan(&mode)
	return mode, e
}
func (s *Store) savePersonCalendarMode(id int64, mode string) error {
	if !validMode(mode) {
		return errors.New("Invalid calendar view")
	}
	_, e := s.db.Exec(`INSERT INTO person_preferences(person_id,calendar_mode) VALUES(?,?) ON CONFLICT(person_id) DO UPDATE SET calendar_mode=excluded.calendar_mode`, id, mode)
	return e
}
