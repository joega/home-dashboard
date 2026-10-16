package dashboard

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"image"
	imgcolor "image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type PersonRow struct {
	Person Person
	CSRF   string
}
type Person struct {
	ID               int64
	Name             string
	Active, HasPhoto bool
	ChoreEligible    bool
	Photo            []byte
	Chores           int
}

func personRef(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
func initials(name string) string {
	parts := strings.Fields(name)
	result := ""
	for _, part := range parts {
		if result != "" && len([]rune(result)) >= 2 {
			break
		}
		result += string([]rune(part)[0])
	}
	return strings.ToUpper(result)
}
func (s *Store) migratePeople() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS people(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT NOT NULL COLLATE NOCASE UNIQUE,active INTEGER NOT NULL DEFAULT 1,photo BLOB)`,
		`CREATE TABLE IF NOT EXISTS chore_history(id INTEGER PRIMARY KEY AUTOINCREMENT,entry_key TEXT NOT NULL,entry_id INTEGER NOT NULL,cycle TEXT NOT NULL,day TEXT NOT NULL,completed_at TEXT NOT NULL,person_id INTEGER REFERENCES people(id),person_name TEXT NOT NULL,title TEXT NOT NULL,UNIQUE(entry_key,cycle))`,
		`CREATE INDEX IF NOT EXISTS chore_history_person_day ON chore_history(person_id,day)`,
		`CREATE INDEX IF NOT EXISTS chore_history_day_person ON chore_history(day,person_id)`,
	} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	rows, err := tx.Query("PRAGMA table_info(entries)")
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !cols["person_id"] {
		if _, err = tx.Exec("ALTER TABLE entries ADD COLUMN person_id INTEGER REFERENCES people(id)"); err != nil {
			return err
		}
	}
	if !cols["entry_key"] {
		if _, err = tx.Exec("ALTER TABLE entries ADD COLUMN entry_key TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("UPDATE entries SET entry_key=lower(hex(randomblob(16))) WHERE entry_key=''"); err != nil {
		return err
	}
	var migrated int
	if err = tx.QueryRow("SELECT COUNT(*) FROM settings WHERE key='people_v1'").Scan(&migrated); err != nil {
		return err
	}
	if migrated == 0 {
		if _, err = tx.Exec(`INSERT INTO people(name) SELECT DISTINCT trim(owner) FROM entries WHERE kind IN ('event','chore') AND trim(owner)<>'' AND lower(trim(owner)) NOT IN ('anyone','household') ON CONFLICT(name) DO NOTHING`); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE entries SET person_id=(SELECT id FROM people WHERE people.name=trim(entries.owner) COLLATE NOCASE) WHERE kind IN ('event','chore') AND person_id IS NULL AND lower(trim(owner)) NOT IN ('anyone','household')`); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES('people_v1','1')"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) People() ([]Person, error) {
	rows, err := s.db.Query(`SELECT id,name,active,chore_eligible,COALESCE(length(photo),0)>0,(SELECT count(*) FROM entries WHERE kind='chore' AND entries.person_id=people.id) FROM people ORDER BY active DESC,name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var people []Person
	for rows.Next() {
		var p Person
		if err = rows.Scan(&p.ID, &p.Name, &p.Active, &p.ChoreEligible, &p.HasPhoto, &p.Chores); err != nil {
			return nil, err
		}
		people = append(people, p)
	}
	return people, rows.Err()
}
func (s *Store) Person(id int64) (Person, error) {
	var p Person
	err := s.db.QueryRow("SELECT id,name,active,chore_eligible,COALESCE(length(photo),0)>0,photo FROM people WHERE id=?", id).Scan(&p.ID, &p.Name, &p.Active, &p.ChoreEligible, &p.HasPhoto, &p.Photo)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrMissing
	}
	return p, err
}
func (s *Store) SavePerson(p Person) error {
	p.Name = strings.TrimSpace(p.Name)
	if len([]rune(p.Name)) < 1 || len([]rune(p.Name)) > 60 {
		return errors.New("Enter a name of 1–60 characters.")
	}
	var duplicate int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM people WHERE name=? COLLATE NOCASE AND id<>?", p.Name, p.ID).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate > 0 {
		return errors.New("That name is already in People. Use a different display name.")
	}
	if p.ID == 0 {
		_, err := s.db.Exec("INSERT INTO people(name,photo) VALUES(?,?)", p.Name, p.Photo)
		return err
	}
	result, err := s.db.Exec("UPDATE people SET name=?,photo=CASE WHEN ? THEN ? ELSE photo END WHERE id=?", p.Name, p.Photo != nil, p.Photo, p.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrMissing
	}
	return err
}
func (s *Store) SetPersonActive(id int64, active bool) error {
	result, err := s.db.Exec("UPDATE people SET active=? WHERE id=?", active, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrMissing
	}
	return err
}

// Store a small, newly encoded image rather than retaining upload metadata or full-size photos.
func avatar(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, (3<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 3<<20 {
		return nil, errors.New("Choose a JPG or PNG photo smaller than 3 MB.")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errors.New("Choose a JPG or PNG photo.")
	}
	if cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return nil, errors.New("Choose a photo under 16 megapixels.")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("That photo could not be read. Try a different JPG or PNG.")
	}
	bounds := src.Bounds()
	side := min(bounds.Dx(), bounds.Dy())
	x0 := bounds.Min.X + (bounds.Dx()-side)/2
	y0 := bounds.Min.Y + (bounds.Dy()-side)/2
	dst := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			r, g, b, a := src.At(x0+x*side/256, y0+y*side/256).RGBA()
			dst.SetRGBA(x, y, imgcolor.RGBA{uint8((r + 65535 - a) >> 8), uint8((g + 65535 - a) >> 8), uint8((b + 65535 - a) >> 8), 255})
		}
	}
	var out bytes.Buffer
	if err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func (a *App) personForm(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = "/settings"
	p.Person = Person{Active: true}
	p.FormTitle = "Add person"
	if r.PathValue("id") != "new" {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p.Person, err = a.store.Person(id)
		if err != nil {
			a.fail(w, err)
			return
		}
		p.FormTitle = "Edit person"
	}
	a.render(w, "person-form", p)
}
func (a *App) savePerson(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = "/settings"
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 0 {
		http.NotFound(w, r)
		return
	}
	p.Person = Person{ID: id, Name: strings.TrimSpace(r.FormValue("name")), Active: true}
	p.FormTitle = "Add person"
	if id != 0 {
		old, e := a.store.Person(id)
		if e != nil {
			a.fail(w, e)
			return
		}
		p.Person.Active = old.Active
		p.Person.HasPhoto = old.HasPhoto
		p.FormTitle = "Edit person"
	}
	if r.FormValue("remove_photo") == "on" {
		p.Person.Photo = []byte{}
		p.Person.HasPhoto = false
	}
	file, _, uploadErr := r.FormFile("photo")
	if uploadErr == nil {
		defer file.Close()
		p.Person.Photo, err = avatar(file)
	} else if uploadErr != http.ErrMissingFile && uploadErr != http.ErrNotMultipart {
		err = errors.New("That photo could not be uploaded. Try again.")
	}
	if err == nil {
		err = a.store.SavePerson(p.Person)
	}
	if err != nil {
		if errors.Is(err, ErrMissing) {
			a.fail(w, err)
			return
		}
		p.Error = err.Error()
		w.Header().Set("HX-Retarget", "#drawer")
		w.Header().Set("HX-Reswap", "innerHTML")
		a.render(w, "person-form", p)
		return
	}
	a.changed(w, r, p)
}
func (a *App) personStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	active := r.FormValue("active")
	if active != "0" && active != "1" {
		http.Error(w, "Invalid person status", 400)
		return
	}
	if err = a.store.SetPersonActive(id, active == "1"); err != nil {
		a.fail(w, err)
		return
	}
	p := a.base(w, r)
	p.Return = "/settings"
	a.changed(w, r, p)
}
func (a *App) personPhoto(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := a.store.Person(id)
	if err != nil || !p.HasPhoto {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", fmt.Sprint(len(p.Photo)))
	w.Write(p.Photo)
}
func (a *App) assignment(e *Entry, r *http.Request) error {
	value := r.FormValue("person_id")
	id, err := strconv.ParseInt(value, 10, 64)
	if value == "" {
		id = 0
		err = nil
	}
	if err != nil || id < 0 {
		return errors.New("Choose a person from the list.")
	}
	e.PersonID = id
	e.Owner = "Anyone"
	if e.Kind == "event" {
		e.Owner = "Household"
	}
	if id == 0 {
		return nil
	}
	person, err := a.store.Person(id)
	if err != nil {
		return errors.New("That person is no longer available. Choose someone else.")
	}
	if e.Kind == "chore" && !person.ChoreEligible {
		return errors.New("That person is not enabled for chores. Choose an eligible person or Household.")
	}
	if !person.Active {
		old, err := a.store.Get(e.ID, time.Now().In(a.loc).Format("2006-01-02"))
		if err != nil || old.PersonID != id {
			return errors.New("Choose an active person from the list.")
		}
	}
	e.Owner = person.Name
	return nil
}
