package dashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type DisplaySettings struct {
	DayUseAndroid   bool   `json:"day_use_android"`
	NightEnabled    bool   `json:"night_enabled"`
	NightStart      string `json:"night_start"`
	NightEnd        string `json:"night_end"`
	DayBrightness   int    `json:"day_brightness"`
	NightBrightness int    `json:"night_brightness"`
	IdleDimEnabled  bool   `json:"idle_dim_enabled"`
	IdleDimMinutes  int    `json:"idle_dim_minutes"`
	IdleBrightness  int    `json:"idle_brightness"`
}

func defaultDisplaySettings() DisplaySettings {
	return DisplaySettings{DayUseAndroid: true, NightEnabled: true, NightStart: "21:00", NightEnd: "07:00", DayBrightness: 55, NightBrightness: 8, IdleDimMinutes: 5, IdleBrightness: 5}
}
func minuteOfDay(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return 0, errors.New("Enter valid start and end times.")
	}
	return t.Hour()*60 + t.Minute(), nil
}
func (d DisplaySettings) Validate() error {
	start, err := minuteOfDay(d.NightStart)
	if err != nil {
		return err
	}
	end, err := minuteOfDay(d.NightEnd)
	if err != nil {
		return err
	}
	if start == end {
		return errors.New("Night start and end must be different.")
	}
	if d.DayBrightness < 1 || d.DayBrightness > 100 || d.NightBrightness < 1 || d.NightBrightness > d.DayBrightness || d.IdleBrightness < 1 || d.IdleBrightness > d.DayBrightness {
		return errors.New("Brightness must be 1–100%. Night and idle brightness cannot exceed daytime brightness.")
	}
	if d.IdleDimMinutes < 1 || d.IdleDimMinutes > 240 {
		return errors.New("Idle dimming must be between 1 and 240 minutes.")
	}
	return nil
}
func (s *Store) migrateDisplay() error {
	raw, _ := json.Marshal(defaultDisplaySettings())
	_, err := s.db.Exec("INSERT INTO settings(key,value) VALUES('display',?) ON CONFLICT(key) DO NOTHING", string(raw))
	return err
}
func (s *Store) DisplaySettings() (DisplaySettings, error) {
	var raw string
	var d DisplaySettings
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key='display'").Scan(&raw); err != nil {
		return d, err
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return d, err
	}
	return d, d.Validate()
}
func (s *Store) SaveDisplaySettings(d DisplaySettings) error {
	if err := d.Validate(); err != nil {
		return err
	}
	raw, _ := json.Marshal(d)
	_, err := s.db.Exec("UPDATE settings SET value=? WHERE key='display'", string(raw))
	return err
}
func (a *App) displayPolicy(w http.ResponseWriter, r *http.Request) {
	d, err := a.store.DisplaySettings()
	if err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Version      int             `json:"version"`
		ServerMillis int64           `json:"server_millis"`
		Zone         string          `json:"zone"`
		Settings     DisplaySettings `json:"settings"`
	}{1, time.Now().UnixMilli(), a.loc.String(), d})
}
func (a *App) saveDisplay(w http.ResponseWriter, r *http.Request) {
	d := DisplaySettings{DayUseAndroid: r.Form.Get("day_use_android") == "on", NightEnabled: r.Form.Get("night_enabled") == "on", NightStart: r.Form.Get("night_start"), NightEnd: r.Form.Get("night_end"), IdleDimEnabled: r.Form.Get("idle_dim_enabled") == "on"}
	d.DayBrightness, _ = strconv.Atoi(r.Form.Get("day_brightness"))
	d.NightBrightness, _ = strconv.Atoi(r.Form.Get("night_brightness"))
	d.IdleDimMinutes, _ = strconv.Atoi(r.Form.Get("idle_dim_minutes"))
	d.IdleBrightness, _ = strconv.Atoi(r.Form.Get("idle_brightness"))
	err := a.store.SaveDisplaySettings(d)
	if r.Header.Get("HX-Request") != "true" && err == nil {
		http.Redirect(w, r, "/settings?section=display", http.StatusSeeOther)
		return
	}
	p := a.base(w, r)
	p.View = "settings"
	p.Return = "/settings?section=display"
	if e := a.load(&p, url.Values{"section": {"display"}}); e != nil {
		a.fail(w, e)
		return
	}
	if err != nil {
		p.DisplaySettings = d
		p.Error = err.Error()
	} else {
		p.Notice = "Display settings saved. The frame applies changes within 30 seconds."
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Push-Url", p.Return)
		a.render(w, "fragment", p)
	} else {
		a.render(w, "page", p)
	}
}
