package dashboard

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"home-dashboard/internal/spotify"
	"time"
)

func (s *Store) migrateSpotify() error {
	_, e := s.db.Exec(`CREATE TABLE IF NOT EXISTS spotify_private(key TEXT PRIMARY KEY,body BLOB NOT NULL)`)
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
		if _, e = tx.Exec("DELETE FROM spotify_private WHERE key='credential'"); e != nil {
			return e
		}
	}
	if _, e = tx.Exec("DELETE FROM spotify_private WHERE key='pending'"); e != nil {
		return e
	}
	raw, _ = json.Marshal(c)
	if _, e = tx.Exec(`INSERT INTO settings(key,value) VALUES('spotify-config',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, raw); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SpotifyCredential() (spotify.Credential, error) {
	var b []byte
	e := s.db.QueryRow("SELECT body FROM spotify_private WHERE key='credential'").Scan(&b)
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
	if e = tx.QueryRow("SELECT body FROM spotify_private WHERE key='credential'").Scan(&b); e != nil {
		return spotify.ErrChanged
	}
	if json.Unmarshal(b, &old) != nil || old.Revision != revision || old.ClientID != c.ClientID {
		return spotify.ErrChanged
	}
	b, _ = json.Marshal(c)
	_, e = tx.Exec("UPDATE spotify_private SET body=? WHERE key='credential'", b)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SaveSpotifyConnected(configRevision string, c spotify.Credential) error {
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
	b, _ = json.Marshal(c)
	_, e = tx.Exec(`INSERT INTO spotify_private(key,body) VALUES('credential',?) ON CONFLICT(key) DO UPDATE SET body=excluded.body`, b)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SaveSpotifyPending(p spotify.Pending) error {
	b, _ := json.Marshal(p)
	_, e := s.db.Exec(`INSERT INTO spotify_private(key,body) VALUES('pending',?) ON CONFLICT(key) DO UPDATE SET body=excluded.body`, b)
	return e
}
func (s *Store) ConsumeSpotifyPending(state, browser, host string) (spotify.Pending, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return spotify.Pending{}, e
	}
	defer tx.Rollback()
	var b []byte
	var p spotify.Pending
	if e = tx.QueryRow("SELECT body FROM spotify_private WHERE key='pending'").Scan(&b); e != nil {
		return p, errors.New("Login expired; start Connect Spotify again")
	}
	if len(b) > 4096 || json.Unmarshal(b, &p) != nil || time.Now().After(p.Expires) || subtle.ConstantTimeCompare([]byte(spotify.Hash(state)), []byte(p.StateHash)) != 1 || subtle.ConstantTimeCompare([]byte(spotify.Hash(browser)), []byte(p.BrowserHash)) != 1 {
		return p, errors.New("Login expired or belongs to another browser; start again")
	}
	cfg := spotify.Config{ClientID: p.ClientID, Redirect: p.Redirect}
	if cfg.Validate() != nil || !cfg.MatchesHost(host) {
		return p, errors.New("Complete Spotify login at the configured loopback address")
	}
	if _, e = tx.Exec("DELETE FROM spotify_private WHERE key='pending'"); e != nil {
		return p, e
	}
	return p, tx.Commit()
}
func (s *Store) DisconnectSpotify() error { _, e := s.db.Exec("DELETE FROM spotify_private"); return e }
