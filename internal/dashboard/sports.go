package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"home-dashboard/internal/sports"
	"net/http"
	"net/url"
)

func (s *Store) migrateSports() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{"CREATE TABLE IF NOT EXISTS sports_cache(team TEXT PRIMARY KEY,body BLOB NOT NULL)", "CREATE TABLE IF NOT EXISTS sports_logos(team TEXT PRIMARY KEY,body BLOB NOT NULL)"} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	defaults, _ := json.Marshal(sports.Defaults())
	if _, err = tx.Exec("INSERT OR IGNORE INTO settings(key,value) VALUES('sports',?)", string(defaults)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) SportsSettings() (sports.Settings, error) {
	var raw string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key='sports'").Scan(&raw); err != nil {
		return sports.Settings{}, err
	}
	var cfg sports.Settings
	if json.Unmarshal([]byte(raw), &cfg) != nil || cfg.Validate() != nil {
		return sports.Defaults(), nil
	}
	return cfg, nil
}
func (s *Store) SaveSportsSettings(cfg sports.Settings) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	body, _ := json.Marshal(cfg)
	_, err := s.db.Exec("UPDATE settings SET value=? WHERE key='sports'", string(body))
	return err
}
func (s *Store) LoadSportsCache(key string) (*sports.Snapshot, error) {
	var raw []byte
	err := s.db.QueryRow("SELECT body FROM sports_cache WHERE team=?", key).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	team, ok := sports.Find(key)
	if !ok || len(raw) > 512<<10 {
		return nil, nil
	}
	var snapshot sports.Snapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Validate(team) != nil {
		return nil, nil
	}
	return &snapshot, nil
}
func (s *Store) SaveSportsCache(snapshot sports.Snapshot) error {
	team, ok := sports.Find(snapshot.TeamKey)
	if !ok {
		return fmt.Errorf("unknown sports team")
	}
	if err := snapshot.Validate(team); err != nil {
		return err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO sports_cache(team,body) VALUES(?,?) ON CONFLICT(team) DO UPDATE SET body=excluded.body", snapshot.TeamKey, raw)
	return err
}
func (s *Store) LoadSportsLogo(key string) ([]byte, error) {
	var b []byte
	err := s.db.QueryRow("SELECT body FROM sports_logos WHERE team=?", key).Scan(&b)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}
func (s *Store) SaveSportsLogo(key string, b []byte) error {
	if _, ok := sports.Find(key); !ok || len(b) == 0 || len(b) > 1<<20 {
		return fmt.Errorf("invalid sports logo")
	}
	_, err := s.db.Exec("INSERT INTO sports_logos(team,body) VALUES(?,?) ON CONFLICT(team) DO UPDATE SET body=excluded.body", key, b)
	return err
}
func (a *App) RunSports(ctx context.Context) { a.sports.Run(ctx) }
func (a *App) loadSports(p *Page, q url.Values) error {
	cfg, err := a.store.SportsSettings()
	if err != nil {
		return err
	}
	p.SportsSettings = cfg
	p.SportsTeams = cfg.Selected()
	p.SportsShortcuts = p.SportsTeams[:min(6, len(p.SportsTeams))]
	p.SportsMore = p.SportsTeams[min(6, len(p.SportsTeams)):]
	for _, t := range sports.Teams() {
		if !cfg.Has(t.Key) {
			p.SportsCatalog = append(p.SportsCatalog, t)
		}
	}
	p.Sports = sports.MakeView(a.sports.States(cfg, p.Now), cfg, q.Get("team"), q.Get("view"), p.Now)
	return nil
}
func (a *App) saveSportsSettings(w http.ResponseWriter, r *http.Request) {
	cfg := sports.Settings{Teams: r.Form["teams"], Today: r.Form.Has("today")}
	if err := a.store.SaveSportsSettings(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.sports.Wake()
	p := a.base(w, r)
	p.Return = "/settings?section=sports"
	p.Notice = "Sports settings saved."
	a.changed(w, r, p)
}
func (a *App) sportsLogo(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("team")
	t, ok := sports.Find(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := a.store.LoadSportsLogo(key)
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if len(b) > 0 {
		w.Header().Set("Content-Type", "image/png")
		w.Write(b)
		return
	}
	// Reviewed initials are also served locally while a provider/logo is unavailable.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "image/svg+xml")
	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 80 80"><rect width="80" height="80" rx="18" fill="#e5e9e6"/><text x="40" y="48" text-anchor="middle" fill="#34443c" font-family="sans-serif" font-size="23" font-weight="bold">%s</text></svg>`, t.Abbr)
}
