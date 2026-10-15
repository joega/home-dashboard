package dashboard

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrNoteConflict = errors.New("This note changed on another device. Load the latest version before saving.")

type noteInputError string

func (e noteInputError) Error() string { return string(e) }

type NoteColor struct{ Key, Name string }

var noteColors = []NoteColor{{"butter", "Butter"}, {"sage", "Sage"}, {"peach", "Peach"}, {"sky", "Sky"}, {"lilac", "Lilac"}}

const notesPerPage = 30

type Note struct {
	ID, AuthorID, RecipientID                     int64
	Title, Body, Color, Author, Recipient         string
	Pinned, Archived, AuthorPhoto, RecipientPhoto bool
	Version                                       int
	CreatedAt, UpdatedAt, ArchivedAt, CreateKey   string
	CreatedLabel, UpdatedLabel                    string
}

func (n Note) Preview() string {
	text := []rune(n.Body)
	if len(text) > 220 {
		return string(text[:220]) + "…"
	}
	return n.Body
}
func (s *Store) migrateNotes() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS notes(
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 title TEXT NOT NULL, body TEXT NOT NULL,
 color TEXT NOT NULL CHECK(color IN ('butter','sage','peach','sky','lilac')),
 author_id INTEGER REFERENCES people(id) ON DELETE SET NULL,author_name TEXT NOT NULL DEFAULT '',
 recipient_id INTEGER REFERENCES people(id) ON DELETE SET NULL,recipient_name TEXT NOT NULL DEFAULT '',
 pinned INTEGER NOT NULL DEFAULT 0 CHECK(pinned IN (0,1)),
 archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0,1)),
 version INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,archived_at TEXT NOT NULL DEFAULT '',
 create_key TEXT NOT NULL UNIQUE)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS notes_board_order ON notes(archived,pinned DESC,created_at DESC,id DESC)`)
	return err
}

const noteSelect = `SELECT n.id,n.title,n.body,n.color,COALESCE(n.author_id,0),COALESCE(NULLIF(a.name,''),NULLIF(n.author_name,''),'Household'),COALESCE(length(a.photo),0)>0,COALESCE(n.recipient_id,0),COALESCE(r.name,n.recipient_name),COALESCE(length(r.photo),0)>0,n.pinned,n.archived,n.version,n.created_at,n.updated_at,n.archived_at FROM notes n LEFT JOIN people a ON a.id=n.author_id LEFT JOIN people r ON r.id=n.recipient_id `

type noteScanner interface{ Scan(...any) error }

func scanNote(row noteScanner) (Note, error) {
	var n Note
	err := row.Scan(&n.ID, &n.Title, &n.Body, &n.Color, &n.AuthorID, &n.Author, &n.AuthorPhoto, &n.RecipientID, &n.Recipient, &n.RecipientPhoto, &n.Pinned, &n.Archived, &n.Version, &n.CreatedAt, &n.UpdatedAt, &n.ArchivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrMissing
	}
	return n, err
}
func (s *Store) Note(id int64) (Note, error) {
	return scanNote(s.db.QueryRow(noteSelect+`WHERE n.id=?`, id))
}
func (s *Store) Notes(archived bool, page int) ([]Note, error) {
	if page < 1 || page > 1000000 {
		return nil, noteInputError("Invalid notes page.")
	}
	order := `n.pinned DESC,n.created_at DESC,n.id DESC`
	if archived {
		order = `n.archived_at DESC,n.id DESC`
	}
	rows, err := s.db.Query(noteSelect+`WHERE n.archived=? ORDER BY `+order+` LIMIT ? OFFSET ?`, archived, notesPerPage, (page-1)*notesPerPage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}
func (s *Store) NoteCounts() (board, archived int, err error) {
	err = s.db.QueryRow(`SELECT COALESCE(SUM(archived=0),0),COALESCE(SUM(archived=1),0) FROM notes`).Scan(&board, &archived)
	return
}
func normalizeNote(n *Note) error {
	n.Title = strings.Join(strings.Fields(n.Title), " ")
	n.Body = strings.TrimSpace(strings.ReplaceAll(n.Body, "\r\n", "\n"))
	if n.Title == "" || !textBound(n.Title, 80) {
		return noteInputError("Enter a title of 1–80 characters.")
	}
	if n.Body == "" || !textBound(n.Body, 4000) {
		return noteInputError("Enter a message of 1–4,000 characters.")
	}
	valid := false
	for _, c := range noteColors {
		valid = valid || n.Color == c.Key
	}
	if !valid {
		return noteInputError("Choose a note color.")
	}
	if n.RecipientID < 0 {
		return noteInputError("Choose a recipient from the list.")
	}
	return nil
}
func noteTimestamp(now time.Time) string { return now.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func (s *Store) SaveNote(n Note, actor Person, now time.Time) (int64, error) {
	if err := normalizeNote(&n); err != nil {
		return 0, err
	}
	if n.ID < 0 {
		return 0, ErrMissing
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	actor, err = actorInTransaction(tx, actor)
	if err != nil {
		return 0, noteInputError("Choose an active current person before saving.")
	}
	var previousRecipient int64
	if n.ID > 0 {
		var version int
		var archived bool
		err = tx.QueryRow(`SELECT version,archived,COALESCE(recipient_id,0) FROM notes WHERE id=?`, n.ID).Scan(&version, &archived, &previousRecipient)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrMissing
		}
		if err != nil {
			return 0, err
		}
		if version != n.Version {
			return 0, ErrNoteConflict
		}
		if archived {
			return 0, noteInputError("Restore this note before editing it.")
		}
	} else {
		if n.CreateKey == "" {
			n.CreateKey = randomToken()
		}
		if !textBound(n.CreateKey, 140) {
			return 0, noteInputError("Reload the new-note form and try again.")
		}
		var existing int64
		err = tx.QueryRow(`SELECT id FROM notes WHERE create_key=?`, n.CreateKey).Scan(&existing)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	recipient := ""
	if n.RecipientID > 0 {
		var active bool
		err = tx.QueryRow(`SELECT name,active FROM people WHERE id=?`, n.RecipientID).Scan(&recipient, &active)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !active && (n.ID == 0 || n.RecipientID != previousRecipient) {
			return 0, noteInputError("Choose an active recipient from Settings → People.")
		}
		if err != nil {
			return 0, err
		}
	}
	stamp := noteTimestamp(now)
	if n.ID == 0 {
		result, err := tx.Exec(`INSERT INTO notes(title,body,color,author_id,author_name,recipient_id,recipient_name,pinned,created_at,updated_at,create_key) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, n.Title, n.Body, n.Color, personRef(actor.ID), actor.Name, personRef(n.RecipientID), recipient, n.Pinned, stamp, stamp, n.CreateKey)
		if err != nil {
			return 0, err
		}
		n.ID, err = result.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else {
		result, err := tx.Exec(`UPDATE notes SET title=?,body=?,color=?,recipient_id=?,recipient_name=?,pinned=?,updated_at=?,version=version+1 WHERE id=? AND version=? AND archived=0`, n.Title, n.Body, n.Color, personRef(n.RecipientID), recipient, n.Pinned, stamp, n.ID, n.Version)
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if count != 1 {
			return 0, ErrNoteConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return n.ID, nil
}
func (s *Store) SetNoteState(id int64, version int, action string, value bool, actor Person, now time.Time) error {
	if id <= 0 {
		return ErrMissing
	}
	if action != "pinned" && action != "archived" {
		return noteInputError("Invalid note action.")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = actorInTransaction(tx, actor); err != nil {
		return noteInputError("Choose an active current person before saving.")
	}
	var oldVersion int
	var archived bool
	err = tx.QueryRow(`SELECT version,archived FROM notes WHERE id=?`, id).Scan(&oldVersion, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMissing
	}
	if err != nil {
		return err
	}
	if oldVersion != version {
		return ErrNoteConflict
	}
	if action == "pinned" && archived {
		return noteInputError("Restore this note before pinning it.")
	}
	var result sql.Result
	if action == "pinned" {
		result, err = tx.Exec(`UPDATE notes SET pinned=?,version=version+1 WHERE id=? AND version=?`, value, id, version)
	} else {
		stamp := ""
		if value {
			stamp = noteTimestamp(now)
		}
		result, err = tx.Exec(`UPDATE notes SET archived=?,archived_at=?,version=version+1 WHERE id=? AND version=?`, value, stamp, id, version)
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNoteConflict
	}
	return tx.Commit()
}
