package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"home-dashboard/internal/weather"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Store) migrateWeather() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS weather_cache(location_key TEXT PRIMARY KEY,body TEXT NOT NULL)`); err != nil {
		return err
	}
	raw, _ := json.Marshal(weather.DefaultSettings())
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES('weather',?) ON CONFLICT(key) DO NOTHING`, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) WeatherSettings() (weather.Settings, error) {
	var raw string
	var cfg weather.Settings
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key='weather'").Scan(&raw); err != nil {
		return cfg, err
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, err
	}
	if cfg.Theme == "" {
		cfg.Theme = "auto"
	}
	return cfg, cfg.Validate()
}
func (s *Store) SaveWeatherSettings(cfg weather.Settings) error {
	if cfg.Theme == "" {
		cfg.Theme = "auto"
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE settings SET value=? WHERE key='weather'", string(raw)); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM weather_cache WHERE location_key<>?", cfg.Location.Key()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) LoadWeatherCache(l weather.Location) (*weather.Forecast, error) {
	var raw string
	err := s.db.QueryRow("SELECT body FROM weather_cache WHERE location_key=?", l.Key()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f weather.Forecast
	if json.Unmarshal([]byte(raw), &f) != nil || f.Validate(time.Now()) != nil || f.Location.Key() != l.Key() {
		return nil, nil
	}
	return &f, nil
}
func (s *Store) SaveWeatherCache(f weather.Forecast) error {
	if err := f.Validate(time.Now()); err != nil {
		return err
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	// Guard a slow response against a ZIP change made while its request was running.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var saved string
	if err = tx.QueryRow("SELECT value FROM settings WHERE key='weather'").Scan(&saved); err != nil {
		return err
	}
	var cfg weather.Settings
	if err = json.Unmarshal([]byte(saved), &cfg); err != nil {
		return err
	}
	if cfg.Location.Key() != f.Location.Key() {
		return nil
	}
	if _, err = tx.Exec("INSERT INTO weather_cache(location_key,body) VALUES(?,?) ON CONFLICT(location_key) DO UPDATE SET body=excluded.body", f.Location.Key(), string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
func (a *App) RunWeather(ctx context.Context) { a.weather.Run(ctx) }
func (a *App) saveWeather(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.store.WeatherSettings()
	if err != nil {
		a.fail(w, err)
		return
	}
	proposed := cfg
	zip := strings.TrimSpace(r.Form.Get("zip"))
	proposed.Units = r.Form.Get("units")
	proposed.Background = r.Form.Get("background")
	if v := r.Form.Get("theme"); v != "" {
		proposed.Theme = v
	}
	proposed.Enabled = r.Form.Get("enabled") == "on"
	if !weather.ValidZIP(zip) {
		err = errors.New("Enter a five-digit US ZIP code.")
	} else {
		err = proposed.Validate()
	}
	if err == nil && zip != cfg.Location.ZIP {
		proposed.Location, err = a.weather.Provider.ResolveZIP(r.Context(), zip)
	}
	if err == nil {
		err = a.store.SaveWeatherSettings(proposed)
	}
	if err != nil {
		p := a.base(w, r)
		p.View = "settings"
		p.Return = "/settings?section=weather"
		if e := a.load(&p, url.Values{"section": {"weather"}}); e != nil {
			a.fail(w, e)
			return
		}
		proposed.Location.ZIP = zip
		p.WeatherSettings = proposed
		p.Error = err.Error()
		if r.Header.Get("HX-Request") == "true" {
			a.render(w, "fragment", p)
		} else {
			a.render(w, "page", p)
		}
		return
	}
	a.weather.Wake()
	if r.Header.Get("HX-Request") == "true" {
		p := a.base(w, r)
		p.View = "settings"
		p.Return = "/settings?section=weather"
		if err = a.load(&p, url.Values{"section": {"weather"}}); err != nil {
			a.fail(w, err)
			return
		}
		p.Notice = "Weather settings saved."
		w.Header().Set("HX-Push-Url", p.Return)
		a.render(w, "fragment", p)
	} else {
		http.Redirect(w, r, "/settings?section=weather", http.StatusSeeOther)
	}
}

// This small response updates only the CSS theme, preserving open editors.
func (a *App) appearance(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.store.WeatherSettings()
	if err != nil {
		a.fail(w, err)
		return
	}
	data, _ := json.Marshal(map[string]any{"appearance": map[string]string{"theme": weather.Appearance(cfg, time.Now())}})
	w.Header().Set("HX-Trigger", string(data))
	w.WriteHeader(http.StatusNoContent)
}
