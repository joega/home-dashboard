package dashboard

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"home-dashboard/internal/spotify"
	"strconv"
	"strings"
	"time"
)

func (s *Store) migrateSpotify() error {
	_, e := s.db.Exec(`CREATE TABLE IF NOT EXISTS spotify_private(key TEXT PRIMARY KEY,body BLOB NOT NULL)`)
	if e != nil {
		return e
	}
	if _, e = s.db.Exec("DELETE FROM spotify_private WHERE key='pending'"); e != nil {
		return e
	}
	_, e = s.db.Exec(`CREATE TABLE IF NOT EXISTS spotify_profile_epochs(person_id INTEGER PRIMARY KEY,revision TEXT NOT NULL)`)
	return e
}
func (s *Store) SpotifyConfig() (spotify.Config, error) {
	var raw []byte
	e := s.db.QueryRow("SELECT value FROM settings WHERE key='spotify-config'").Scan(&raw)
	if e == sql.ErrNoRows {
		return spotify.Config{Redirect: "http://127.0.0.1:4182" + spotify.CallbackPath}, nil
	}
	if e != nil {
		return spotify.Config{}, e
	}
	var c spotify.Config
	if len(raw) > 4096 || json.Unmarshal(raw, &c) != nil {
		return c, errors.New("Invalid Spotify settings")
	}
	return c, nil
}
func (s *Store) SaveSpotifyConfig(c spotify.Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	c.Revision = spotify.Random()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw []byte
	var old spotify.Config
	tx.QueryRow("SELECT value FROM settings WHERE key='spotify-config'").Scan(&raw)
	json.Unmarshal(raw, &old)
	if old.ClientID != c.ClientID {
		if _, e = tx.Exec("DELETE FROM spotify_private WHERE key='credential' OR key LIKE 'credential:%'"); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("DELETE FROM spotify_private WHERE key='pending' OR key LIKE 'pending:%'"); e != nil {
		return e
	}
	raw, _ = json.Marshal(c)
	if _, e = tx.Exec(`INSERT INTO settings(key,value) VALUES('spotify-config',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, raw); e != nil {
		return e
	}
	return tx.Commit()
}
func credentialKey(id int64) string {
	if id == 0 {
		return "credential"
	}
	return "credential:" + strconv.FormatInt(id, 10)
}
func (s *Store) SpotifyCredential() (spotify.Credential, error) { return s.SpotifyCredentialFor(0) }
func (s *Store) SpotifyCredentialFor(personID int64) (spotify.Credential, error) {
	var b []byte
	e := s.db.QueryRow("SELECT body FROM spotify_private WHERE key=?", credentialKey(personID)).Scan(&b)
	if e == sql.ErrNoRows {
		return spotify.Credential{}, nil
	}
	if e != nil {
		return spotify.Credential{}, e
	}
	var c spotify.Credential
	if len(b) > 16384 || json.Unmarshal(b, &c) != nil {
		return c, errors.New("Invalid saved Spotify connection")
	}
	return c, nil
}
func validSpotifyCredential(c spotify.Credential) bool {
	return c.Connected() && len(c.AccessToken) > 0 && len(c.AccessToken) <= 4096 && len(c.RefreshToken) <= 4096 && len(c.Scope) <= 2048 && len(c.Revision) > 0 && len(c.Revision) <= 128 && len(c.AccountName) <= 320 && len(c.AccountID) <= 400 && c.Expires.After(time.Now())
}
func (s *Store) SwapSpotifyCredential(revision string, c spotify.Credential) error {
	return s.SwapSpotifyCredentialFor(0, revision, c)
}
func (s *Store) SwapSpotifyCredentialFor(personID int64, revision string, c spotify.Credential) error {
	if !validSpotifyCredential(c) {
		return errors.New("Invalid Spotify credential")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var b []byte
	var old spotify.Credential
	if e = tx.QueryRow("SELECT body FROM spotify_private WHERE key=?", credentialKey(personID)).Scan(&b); e != nil {
		return spotify.ErrChanged
	}
	if json.Unmarshal(b, &old) != nil || old.Revision != revision || old.ClientID != c.ClientID || old.AccountID != c.AccountID {
		return spotify.ErrChanged
	}
	b, _ = json.Marshal(c)
	_, e = tx.Exec("UPDATE spotify_private SET body=? WHERE key=?", b, credentialKey(personID))
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SaveSpotifyConnected(configRevision string, c spotify.Credential) error {
	return s.SaveSpotifyConnectedFor(0, configRevision, "", c)
}
func (s *Store) SaveSpotifyConnectedFor(personID int64, configRevision, linkRevision string, c spotify.Credential) error {
	if !validSpotifyCredential(c) {
		return errors.New("Invalid Spotify credential")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var b []byte
	var cfg spotify.Config
	if e = tx.QueryRow("SELECT value FROM settings WHERE key='spotify-config'").Scan(&b); e != nil {
		return spotify.ErrChanged
	}
	if json.Unmarshal(b, &cfg) != nil || cfg.Revision != configRevision || cfg.ClientID != c.ClientID {
		return spotify.ErrChanged
	}
	if e = validSpotifyPerson(tx, personID); e != nil {
		return e
	}
	if linkRevision != "" {
		var current string
		if e = tx.QueryRow("SELECT revision FROM spotify_profile_epochs WHERE person_id=?", personID).Scan(&current); e != nil || current != linkRevision {
			return spotify.ErrChanged
		}
	}
	if e = uniqueSpotifyAccount(tx, personID, c.AccountID); e != nil {
		return e
	}
	b, _ = json.Marshal(c)
	_, e = tx.Exec(`INSERT INTO spotify_private(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body`, credentialKey(personID), b)
	if e != nil {
		return e
	}
	if e = rotateSpotifyEpoch(tx, personID); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SaveSpotifyPending(p spotify.Pending) error {
	b, _ := json.Marshal(p)
	if len(b) > 4096 {
		return errors.New("Invalid Spotify login")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	rows, e := tx.Query("SELECT key,body FROM spotify_private WHERE key='pending' OR key LIKE 'pending:%'")
	if e != nil {
		return e
	}
	expired := []string{}
	count := 0
	for rows.Next() {
		var key string
		var raw []byte
		if e = rows.Scan(&key, &raw); e != nil {
			rows.Close()
			return e
		}
		var old spotify.Pending
		if len(raw) > 4096 || json.Unmarshal(raw, &old) != nil || time.Now().After(old.Expires) {
			expired = append(expired, key)
		} else {
			count++
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, key := range expired {
		if _, e = tx.Exec("DELETE FROM spotify_private WHERE key=?", key); e != nil {
			return e
		}
	}
	if count >= 32 {
		return errors.New("Too many pending Spotify logins. Wait for earlier logins to expire")
	}
	if _, e = tx.Exec(`INSERT INTO spotify_private(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body`, pendingKey(p), b); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) ConsumeSpotifyPending(state, browser, host string) (spotify.Pending, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return spotify.Pending{}, e
	}
	defer tx.Rollback()
	var b []byte
	var p spotify.Pending
	if e = tx.QueryRow("SELECT body FROM spotify_private WHERE key LIKE 'pending:%:' || ?", spotify.Hash(state)).Scan(&b); e != nil {
		return p, errors.New("Login expired; start Connect Spotify again")
	}
	if len(b) > 4096 || json.Unmarshal(b, &p) != nil || time.Now().After(p.Expires) || subtle.ConstantTimeCompare([]byte(spotify.Hash(state)), []byte(p.StateHash)) != 1 || subtle.ConstantTimeCompare([]byte(spotify.Hash(browser)), []byte(p.BrowserHash)) != 1 {
		return p, errors.New("Login expired or belongs to another browser; start again")
	}
	cfg := spotify.Config{ClientID: p.ClientID, Redirect: p.Redirect}
	if cfg.Validate() != nil || !cfg.MatchesHost(host) {
		return p, errors.New("Complete Spotify login at the configured loopback address")
	}
	if _, e = tx.Exec("DELETE FROM spotify_private WHERE key=?", pendingKey(p)); e != nil {
		return p, e
	}
	return p, tx.Commit()
}
func (s *Store) DisconnectSpotify() error { return s.DisconnectSpotifyFor(0) }
func (s *Store) DisconnectSpotifyFor(id int64) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM spotify_private WHERE key=? OR key LIKE ?", credentialKey(id), "pending:"+strconv.FormatInt(id, 10)+":%"); e != nil {
		return e
	}
	if e = rotateSpotifyEpoch(tx, id); e != nil {
		return e
	}
	return tx.Commit()
}

func pendingKey(p spotify.Pending) string {
	return "pending:" + strconv.FormatInt(p.PersonID, 10) + ":" + p.StateHash
}
func validSpotifyPerson(tx *sql.Tx, id int64) error {
	if id == 0 {
		return nil
	}
	var active bool
	if id < 0 {
		return errors.New("Choose an active person for Spotify")
	}
	if e := tx.QueryRow("SELECT active FROM people WHERE id=?", id).Scan(&active); e != nil || !active {
		return errors.New("Choose an active person for Spotify")
	}
	return nil
}
func uniqueSpotifyAccount(tx *sql.Tx, personID int64, accountID string) error {
	if accountID == "" {
		return nil
	} // Retains compatibility with older saved credentials.
	rows, e := tx.Query("SELECT key,body FROM spotify_private WHERE key='credential' OR key LIKE 'credential:%'")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var body []byte
		if e = rows.Scan(&key, &body); e != nil {
			return e
		}
		var c spotify.Credential
		if key != credentialKey(personID) && json.Unmarshal(body, &c) == nil && c.AccountID == accountID {
			return errors.New("This Spotify account is already linked. Each person needs their own account")
		}
	}
	return rows.Err()
}
func rotateSpotifyEpoch(tx *sql.Tx, id int64) error {
	_, e := tx.Exec(`INSERT INTO spotify_profile_epochs(person_id,revision) VALUES(?,?) ON CONFLICT(person_id) DO UPDATE SET revision=excluded.revision`, id, spotify.Random())
	return e
}
func (s *Store) SpotifyLinkRevision(id int64) (string, error) {
	_, e := s.db.Exec("INSERT INTO spotify_profile_epochs(person_id,revision) VALUES(?,?) ON CONFLICT DO NOTHING", id, spotify.Random())
	if e != nil {
		return "", e
	}
	var revision string
	e = s.db.QueryRow("SELECT revision FROM spotify_profile_epochs WHERE person_id=?", id).Scan(&revision)
	return revision, e
}
func (s *Store) SpotifyProfileIDs() ([]int64, error) {
	rows, e := s.db.Query("SELECT key FROM spotify_private WHERE key='credential' OR key LIKE 'credential:%' ORDER BY key")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			return nil, e
		}
		if key == "credential" {
			ids = append(ids, 0)
		} else if id, e := strconv.ParseInt(strings.TrimPrefix(key, "credential:"), 10, 64); e == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}
func (s *Store) AssignLegacySpotify(id int64) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if id <= 0 {
		return errors.New("Choose a person")
	}
	if e = validSpotifyPerson(tx, id); e != nil {
		return e
	}
	var exists bool
	if e = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM spotify_private WHERE key=?)", credentialKey(id)).Scan(&exists); e != nil {
		return e
	}
	if exists {
		return errors.New("That person already has a Spotify connection")
	}
	var raw []byte
	var c spotify.Credential
	if e = tx.QueryRow("SELECT body FROM spotify_private WHERE key='credential'").Scan(&raw); e != nil {
		return spotify.ErrChanged
	}
	if json.Unmarshal(raw, &c) != nil {
		return spotify.ErrChanged
	}
	c.Revision = spotify.Random()
	raw, _ = json.Marshal(c)
	if _, e = tx.Exec("INSERT INTO spotify_private(key,body) VALUES(?,?)", credentialKey(id), raw); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM spotify_private WHERE key='credential' OR key LIKE 'pending:0:%' OR key LIKE ?", "pending:"+strconv.FormatInt(id, 10)+":%"); e != nil {
		return e
	}
	for _, personID := range []int64{0, id} {
		if e = rotateSpotifyEpoch(tx, personID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
