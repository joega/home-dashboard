package dashboard

import (
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Entry struct {
	ShopID                                                  int64
	ShopName                                                string
	Repeat, RepeatUntil, Location                           string
	RepeatEvery, RepeatDays                                 int
	Due                                                     bool
	ID, PersonID                                            int64
	Kind, Title, Owner, Routine, Quantity, Date, Start, End string
	Daily, Done, HasPhoto                                   bool
}
type Store struct{ db *sql.DB }

var ErrMissing = errors.New("entry not found")
var ErrNotDue = errors.New("chore is not scheduled today")

func Open(path string, demo bool, today string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000",
		"CREATE TABLE IF NOT EXISTS entries(id INTEGER PRIMARY KEY,kind TEXT NOT NULL CHECK(kind IN ('event','chore','shopping')),title TEXT NOT NULL,owner TEXT NOT NULL DEFAULT 'Anyone',routine TEXT NOT NULL DEFAULT 'Morning',quantity TEXT NOT NULL DEFAULT '',date TEXT NOT NULL DEFAULT '',start TEXT NOT NULL DEFAULT '',end TEXT NOT NULL DEFAULT '',daily INTEGER NOT NULL DEFAULT 0,done INTEGER NOT NULL DEFAULT 0)",
		"CREATE TABLE IF NOT EXISTS completions(entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,day TEXT NOT NULL,PRIMARY KEY(entry_id,day))",
		"CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL)",
		"CREATE INDEX IF NOT EXISTS entries_kind_date ON entries(kind,date)",
	} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &Store{db}
	if err := s.migratePeople(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateRecurrence(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateShops(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateWeather(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateDisplay(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateSports(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateMedia(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateRecipes(); err != nil {
		db.Close()
		return nil, err
	}
	var initialized int
	db.QueryRow("SELECT COUNT(*) FROM settings WHERE key='initialized'").Scan(&initialized)
	if initialized == 0 {
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, err
		}
		if demo {
			seed := []Entry{
				{Kind: "event", Title: "School pickup", Date: today, Start: "15:15", End: "15:45"},
				{Kind: "event", Title: "Dinner together", Date: today, Start: "18:00"},
				{Kind: "event", Title: "Library books due", Date: today},
				{Kind: "chore", Title: "Feed the cat", Owner: "Anyone", Routine: "Morning", Daily: true},
				{Kind: "chore", Title: "Empty dishwasher", Owner: "Anyone", Routine: "Morning", Daily: true},
				{Kind: "chore", Title: "Take out recycling", Owner: "Anyone", Routine: "Evening", Daily: true},
				{Kind: "shopping", Title: "Milk", Quantity: "1 carton"},
				{Kind: "shopping", Title: "Apples", Quantity: "6"},
				{Kind: "shopping", Title: "Bread", Quantity: "1 loaf"},
			}
			for _, e := range seed {
				e = normalizeSchedule(e)
				if _, err = tx.Exec("INSERT INTO entries(kind,title,owner,routine,quantity,date,start,end,daily,person_id,entry_key,repeat,repeat_every,repeat_days,repeat_until,location,shop_id) VALUES(?,?,?,?,?,?,?,?,?,?,lower(hex(randomblob(16))),?,?,?,?,?,?)", e.Kind, e.Title, e.Owner, e.Routine, e.Quantity, e.Date, e.Start, e.End, e.Daily, personRef(e.PersonID), e.Repeat, e.RepeatEvery, e.RepeatDays, e.RepeatUntil, e.Location, personRef(e.ShopID)); err != nil {
					tx.Rollback()
					db.Close()
					return nil, err
				}
			}
		}
		_, err = tx.Exec("INSERT INTO settings(key,value) VALUES('initialized','1'),('demo',?)", fmt.Sprint(demo))
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0600); err != nil && !(suffix != "" && errors.Is(err, os.ErrNotExist)) {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Demo() bool {
	var v string
	s.db.QueryRow("SELECT value FROM settings WHERE key='demo'").Scan(&v)
	return v == "true"
}
func (s *Store) List(kind, day string) ([]Entry, error) {
	rows, err := s.db.Query("SELECT entries.id,kind,title,COALESCE(people.name,owner),routine,quantity,date,start,end,daily,CASE WHEN daily=1 THEN EXISTS(SELECT 1 FROM completions c WHERE c.entry_id=entries.id AND c.day=?) ELSE done END,COALESCE(person_id,0),COALESCE(length(people.photo),0)>0,repeat,repeat_every,repeat_days,repeat_until,location,COALESCE(shop_id,0),COALESCE(shopping_stores.name,'') FROM entries LEFT JOIN people ON people.id=entries.person_id LEFT JOIN shopping_stores ON shopping_stores.id=entries.shop_id WHERE kind=? ORDER BY CASE WHEN kind='event' AND start='' THEN 1 ELSE 0 END,start,entries.id", day, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Kind, &e.Title, &e.Owner, &e.Routine, &e.Quantity, &e.Date, &e.Start, &e.End, &e.Daily, &e.Done, &e.PersonID, &e.HasPhoto, &e.Repeat, &e.RepeatEvery, &e.RepeatDays, &e.RepeatUntil, &e.Location, &e.ShopID, &e.ShopName); err != nil {
			return nil, err
		}
		e.Due = choreDue(e, day)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
func (s *Store) Get(id int64, day string) (Entry, error) {
	var e Entry
	err := s.db.QueryRow("SELECT entries.id,kind,title,COALESCE(people.name,owner),routine,quantity,date,start,end,daily,CASE WHEN daily=1 THEN EXISTS(SELECT 1 FROM completions c WHERE c.entry_id=entries.id AND c.day=?) ELSE done END,COALESCE(person_id,0),COALESCE(length(people.photo),0)>0,repeat,repeat_every,repeat_days,repeat_until,location,COALESCE(shop_id,0),COALESCE(shopping_stores.name,'') FROM entries LEFT JOIN people ON people.id=entries.person_id LEFT JOIN shopping_stores ON shopping_stores.id=entries.shop_id WHERE entries.id=?", day, id).Scan(&e.ID, &e.Kind, &e.Title, &e.Owner, &e.Routine, &e.Quantity, &e.Date, &e.Start, &e.End, &e.Daily, &e.Done, &e.PersonID, &e.HasPhoto, &e.Repeat, &e.RepeatEvery, &e.RepeatDays, &e.RepeatUntil, &e.Location, &e.ShopID, &e.ShopName)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrMissing
	}
	e.Due = choreDue(e, day)
	return e, err
}
func (s *Store) Save(e Entry) error {
	e = normalizeSchedule(e)
	if e.ID == 0 {
		_, err := s.db.Exec("INSERT INTO entries(kind,title,owner,routine,quantity,date,start,end,daily,person_id,entry_key,repeat,repeat_every,repeat_days,repeat_until,location,shop_id) VALUES(?,?,?,?,?,?,?,?,?,?,lower(hex(randomblob(16))),?,?,?,?,?,?)", e.Kind, e.Title, e.Owner, e.Routine, e.Quantity, e.Date, e.Start, e.End, e.Daily, personRef(e.PersonID), e.Repeat, e.RepeatEvery, e.RepeatDays, e.RepeatUntil, e.Location, personRef(e.ShopID))
		return err
	}
	result, err := s.db.Exec("UPDATE entries SET title=?,owner=?,routine=?,quantity=?,date=?,start=?,end=?,daily=?,person_id=?,repeat=?,repeat_every=?,repeat_days=?,repeat_until=?,location=?,shop_id=? WHERE id=? AND kind=?", e.Title, e.Owner, e.Routine, e.Quantity, e.Date, e.Start, e.End, e.Daily, personRef(e.PersonID), e.Repeat, e.RepeatEvery, e.RepeatDays, e.RepeatUntil, e.Location, personRef(e.ShopID), e.ID, e.Kind)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrMissing
	}
	return err
}
func (s *Store) SetDone(id int64, day string, done bool) error {
	return s.SetDoneAt(id, day, done, time.Now())
}
func (s *Store) SetDoneAt(id int64, day string, done bool, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind string
	var daily, wasDone bool
	var key, title, name string
	var personID sql.NullInt64
	var schedule Entry
	if err = tx.QueryRow("SELECT kind,daily,done,entry_key,title,person_id,COALESCE((SELECT name FROM people WHERE people.id=entries.person_id),owner),date,repeat,repeat_every,repeat_days,repeat_until FROM entries WHERE id=?", id).Scan(&kind, &daily, &wasDone, &key, &title, &personID, &name, &schedule.Date, &schedule.Repeat, &schedule.RepeatEvery, &schedule.RepeatDays, &schedule.RepeatUntil); errors.Is(err, sql.ErrNoRows) {
		return ErrMissing
	} else if err != nil {
		return err
	}
	if kind == "event" {
		return errors.New("events cannot be completed")
	}
	schedule.Kind = kind
	schedule.Daily = daily
	if !choreDue(schedule, day) {
		return ErrNotDue
	}
	if daily {
		if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM completions WHERE entry_id=? AND day=?)", id, day).Scan(&wasDone); err != nil {
			return err
		}
		if done {
			_, err = tx.Exec("INSERT INTO completions(entry_id,day) VALUES(?,?) ON CONFLICT DO NOTHING", id, day)
		} else {
			_, err = tx.Exec("DELETE FROM completions WHERE entry_id=? AND day=?", id, day)
		}
	} else {
		_, err = tx.Exec("UPDATE entries SET done=? WHERE id=?", done, id)
	}
	if err != nil {
		return err
	}
	if kind == "chore" {
		cycle := "once"
		if daily {
			cycle = day
		}
		if done && !wasDone {
			_, err = tx.Exec("INSERT INTO chore_history(entry_key,entry_id,cycle,day,completed_at,person_id,person_name,title) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(entry_key,cycle) DO NOTHING", key, id, cycle, day, at.UTC().Format(time.RFC3339Nano), personID, name, title)
		} else if !done {
			_, err = tx.Exec("DELETE FROM chore_history WHERE entry_key=? AND cycle=?", key, cycle)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Delete(id int64) error {
	result, err := s.db.Exec("DELETE FROM entries WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrMissing
	}
	return err
}
func (s *Store) ClearDemo() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v string
	tx.QueryRow("SELECT value FROM settings WHERE key='demo'").Scan(&v)
	if v != "true" {
		return errors.New("example mode is already off")
	}
	if _, err = tx.Exec("DELETE FROM chore_history"); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM entries"); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE settings SET value='false' WHERE key='demo'"); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ClearShopping() error {
	_, err := s.db.Exec("DELETE FROM entries WHERE kind='shopping' AND done=1")
	return err
}
func (s *Store) Backup(path string) error {
	// Reserve a private, empty output before SQLite writes any credential data.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("backup target already exists")
		}
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if _, err = s.db.Exec("VACUUM INTO ?", path); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}
func Validate(e Entry, loc *time.Location) error {
	e = normalizeSchedule(e)
	if err := validSchedule(e); err != nil {
		return err
	}
	if len([]rune(e.Location)) > 160 {
		return errors.New("Keep the location under 160 characters.")
	}
	e.Title = strings.TrimSpace(e.Title)
	if len([]rune(e.Title)) < 1 || len([]rune(e.Title)) > 120 {
		return errors.New("Enter a title of 1–120 characters.")
	}
	for _, value := range []string{e.Owner, e.Quantity} {
		if len([]rune(value)) > 80 {
			return errors.New("Keep names and quantities under 80 characters.")
		}
	}
	switch e.Kind {
	case "event":
		if _, err := time.Parse("2006-01-02", e.Date); err != nil {
			return errors.New("Choose a valid date.")
		}
		if e.Start != "" {
			start, err := time.ParseInLocation("2006-01-02T15:04", e.Date+"T"+e.Start, loc)
			if err != nil || start.Format("15:04") != e.Start {
				return errors.New("Choose a valid start time.")
			}
			if e.End != "" {
				end, err := time.ParseInLocation("2006-01-02T15:04", e.Date+"T"+e.End, loc)
				if err != nil || end.Format("15:04") != e.End || !end.After(start) {
					return errors.New("The end time must be after the start time.")
				}
			}
		} else if e.End != "" {
			return errors.New("Set a start time before an end time.")
		}
	case "chore":
		if e.Routine != "Morning" && e.Routine != "Afternoon" && e.Routine != "Evening" {
			return errors.New("Choose a routine.")
		}
	case "shopping":
	default:
		return errors.New("Unknown item type.")
	}
	return nil
}
