package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"home-dashboard/internal/weather"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type dashboardWeatherProvider struct {
	fetches, resolves int
	resolveFail       bool
}

func (p *dashboardWeatherProvider) ResolveZIP(ctx context.Context, zip string) (weather.Location, error) {
	p.resolves++
	if p.resolveFail {
		return weather.Location{}, errors.New("ZIP lookup is unavailable.")
	}
	l := weather.DefaultSettings().Location
	l.ZIP = zip
	l.Name = "Changed home"
	return l, nil
}
func (p *dashboardWeatherProvider) Fetch(ctx context.Context, l weather.Location, now time.Time) (weather.Forecast, error) {
	p.fetches++
	z, _ := time.LoadLocation(l.Zone)
	mid := time.Date(now.In(z).Year(), now.In(z).Month(), now.In(z).Day(), 0, 0, 0, 0, z)
	rain := 55.
	return weather.Forecast{Location: l, RetrievedAt: now, Current: weather.Sample{Time: now.Add(-15 * time.Minute), Temperature: 20, FeelsLike: 19, CloudCover: 80, Code: 61}, Hourly: []weather.Sample{{Time: now.Add(-time.Hour), Temperature: 20, FeelsLike: 20, Code: 61}, {Time: now.Add(time.Hour), Temperature: 20, FeelsLike: 20, Code: 61}}, Daily: []weather.Daily{{Date: mid.Format("2006-01-02"), Low: 10, High: 23, Code: 61, Sunrise: mid.Add(7 * time.Hour), Sunset: mid.Add(18 * time.Hour), RainChance: &rain}}}, nil
}
func TestWeatherSettingsCacheAndHouseholdPersistence(t *testing.T) {
	s, path := testStore(t)
	s.Save(Entry{Kind: "shopping", Title: "User groceries"})
	cfg, _ := s.WeatherSettings()
	if cfg.Location.ZIP != "02760" || cfg.Units != "F" || !cfg.Enabled {
		t.Fatal(cfg)
	}
	p := &dashboardWeatherProvider{}
	f, err := p.Fetch(context.Background(), cfg.Location, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveWeatherCache(f); err != nil {
		t.Fatal(err)
	}
	cfg.Units = "C"
	cfg.Background = "storm"
	s.SaveWeatherSettings(cfg)
	s.Close()
	s, err = Open(path, true, "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg, _ = s.WeatherSettings()
	cached, err := s.LoadWeatherCache(cfg.Location)
	if err != nil || cached == nil || cfg.Units != "C" || cfg.Background != "storm" {
		t.Fatal("weather settings/cache lost on restart", cfg, err)
	}
	entries, _ := s.List("shopping", "2026-10-13")
	if len(entries) != 1 || entries[0].Title != "User groceries" {
		t.Fatal("weather migration reset household")
	}
	previous := cfg.Location
	cfg.Location.ZIP = "02761"
	s.SaveWeatherSettings(cfg)
	if old, _ := s.LoadWeatherCache(previous); old != nil {
		t.Fatal("old location cache survived change")
	}
	s.SaveWeatherCache(f)
	if old, _ := s.LoadWeatherCache(previous); old != nil {
		t.Fatal("slow previous-location response persisted")
	}
	s.db.Exec("INSERT INTO weather_cache(location_key,body) VALUES(?,?)", cfg.Location.Key(), `{"Location":{},"Current":{}}`)
	if cached, err = s.LoadWeatherCache(cfg.Location); err != nil || cached != nil {
		t.Fatal("corrupt cache not discarded")
	}
}
func TestWeatherHTTPSettingsAndPreview(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	provider := &dashboardWeatherProvider{}
	a, err := New(s, loc, provider)
	if err != nil {
		t.Fatal(err)
	}
	a.weather.Refresh(context.Background(), time.Now())
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test"+path, nil))
		return w
	}
	first := get("/settings?section=weather")
	if first.Code != 200 || !strings.Contains(first.Body.String(), "Home ZIP code") || strings.Contains(first.Body.String(), ">Add person</button>") {
		t.Fatal("weather settings header incorrect")
	}
	cookie := first.Result().Cookies()[0]
	post := func(values url.Values) *httptest.ResponseRecorder {
		values.Set("csrf", cookie.Value)
		r := httptest.NewRequest("POST", "http://household.test/settings/weather", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	values := url.Values{"zip": {"02760"}, "units": {"C"}, "background": {"storm"}, "enabled": {"on"}}
	w := post(values)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Weather settings saved.") || w.Header().Get("HX-Push-Url") != "/settings?section=weather" {
		t.Fatal("settings not saved", w.Body.String())
	}
	body := get("/").Body.String()
	if !strings.Contains(body, `data-weather="storm"`) || !strings.Contains(body, "20°") || !strings.Contains(body, "Rain chance 55%") || !strings.Contains(body, "Background preview: storm") || strings.Contains(body, "Thunderstorms") {
		t.Fatal("scene preview falsified weather")
	}
	for i := 0; i < 10; i++ {
		get("/calendar")
		get("/")
	}
	if provider.fetches != 1 || provider.resolves != 0 {
		t.Fatal("page reads requested weather")
	}
	values.Set("zip", "not-a-zip")
	w = post(values)
	cfg, _ := s.WeatherSettings()
	if !strings.Contains(w.Body.String(), "five-digit") || cfg.Location.ZIP != "02760" {
		t.Fatal("invalid ZIP changed settings")
	}
	values.Set("zip", "02761")
	provider.resolveFail = true
	w = post(values)
	cfg, _ = s.WeatherSettings()
	if !strings.Contains(w.Body.String(), "lookup is unavailable") || cfg.Location.ZIP != "02760" {
		t.Fatal("failed ZIP changed saved home")
	}
	values.Set("zip", "02760")
	values.Set("background", "<script>")
	w = post(values)
	cfg, _ = s.WeatherSettings()
	if cfg.Background != "storm" || !strings.Contains(w.Body.String(), "Choose a background") {
		t.Fatal("unvalidated scene saved")
	}
	values.Set("background", "live")
	values.Del("enabled")
	post(values)
	body = get("/").Body.String()
	if strings.Contains(body, `aria-label="Home weather"`) || !strings.Contains(body, `data-weather="neutral"`) {
		t.Fatal("off setting retained weather")
	}
	bad := httptest.NewRecorder()
	a.ServeHTTP(bad, httptest.NewRequest("POST", "http://household.test/settings/weather", nil))
	if bad.Code != 403 {
		t.Fatal("weather settings missing CSRF protection")
	}
}

func TestAppearanceMigrationAndIndependentRefresh(t *testing.T) {
	s, _ := testStore(t)
	cfg, _ := s.WeatherSettings()
	raw, _ := json.Marshal(cfg)
	var legacy map[string]any
	json.Unmarshal(raw, &legacy)
	delete(legacy, "Theme")
	raw, _ = json.Marshal(legacy)
	s.db.Exec("UPDATE settings SET value=? WHERE key='weather'", string(raw))
	cfg, err := s.WeatherSettings()
	if err != nil || cfg.Theme != "auto" {
		t.Fatal("old weather settings lost", cfg, err)
	}
	cfg.Theme = "dark"
	cfg.Enabled = false
	if err = s.SaveWeatherSettings(cfg); err != nil {
		t.Fatal(err)
	}
	p := &dashboardWeatherProvider{}
	loc, _ := time.LoadLocation("America/New_York")
	a, err := New(s, loc, p)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/appearance", nil))
	if w.Code != 204 || !strings.Contains(w.Header().Get("HX-Trigger"), `"theme":"dark"`) || p.fetches != 0 {
		t.Fatal("appearance refresh touched weather or failed", w.Header())
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/", nil))
	body := w.Body.String()
	if !strings.Contains(body, `data-theme="dark"`) || !strings.Contains(body, `id="appearance-refresh"`) || !strings.Contains(body, `class="topbar"`) {
		t.Fatal("theme/menu missing")
	}
}
