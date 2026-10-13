package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDisplaySettingsPersistenceAndValidation(t *testing.T) {
	s, path := testStore(t)
	d, err := s.DisplaySettings()
	if err != nil || !d.NightEnabled || d.IdleDimEnabled {
		t.Fatal(d, err)
	}
	d.NightStart = "22:30"
	d.NightBrightness = 3
	d.IdleDimEnabled = true
	if err = s.SaveDisplaySettings(d); err != nil {
		t.Fatal(err)
	}
	bad := d
	bad.NightEnd = bad.NightStart
	if s.SaveDisplaySettings(bad) == nil {
		t.Fatal("all day schedule accepted")
	}
	bad = d
	bad.IdleDimMinutes = 0
	if s.SaveDisplaySettings(bad) == nil {
		t.Fatal("zero idle accepted")
	}
	bad = d
	bad.NightBrightness = 101
	if s.SaveDisplaySettings(bad) == nil {
		t.Fatal("brightness accepted")
	}
	s.Close()
	s, err = Open(path, false, "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.DisplaySettings()
	if err != nil || got != d {
		t.Fatal("settings did not persist", got, err)
	}
}
func TestDisplayHTTPSettingsAndPolicy(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/settings?section=display", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Save display settings") || strings.Contains(w.Body.String(), ">Add person</button>") {
		t.Fatal(w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	v := url.Values{"csrf": {cookie.Value}, "night_enabled": {"on"}, "night_start": {"22:00"}, "night_end": {"06:30"}, "day_brightness": {"55"}, "night_brightness": {"4"}, "idle_dim_minutes": {"5"}, "idle_brightness": {"3"}}
	post := func(v url.Values, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://household.test/settings/display", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		if csrf {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if post(v, false).Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	w = post(v, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Display settings saved.") {
		t.Fatal(w.Body.String())
	}
	v.Set("night_start", "24:00")
	w = post(v, true)
	if !strings.Contains(w.Body.String(), "valid start and end") {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/frame/display-policy", nil))
	var payload struct {
		Version      int             `json:"version"`
		ServerMillis int64           `json:"server_millis"`
		Zone         string          `json:"zone"`
		Settings     DisplaySettings `json:"settings"`
	}
	if json.Unmarshal(w.Body.Bytes(), &payload) != nil || payload.Version != 1 || payload.Zone != "America/New_York" || payload.Settings.NightStart != "22:00" || time.Since(time.UnixMilli(payload.ServerMillis)) > time.Second {
		t.Fatal("invalid policy", w.Body.String())
	}
}
