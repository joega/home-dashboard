package weather

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func fixture(now time.Time) Forecast {
	l := DefaultSettings().Location
	z, _ := time.LoadLocation(l.Zone)
	mid := time.Date(now.In(z).Year(), now.In(z).Month(), now.In(z).Day(), 0, 0, 0, 0, z)
	rain := 40.
	f := Forecast{Location: l, RetrievedAt: now, Current: Sample{Time: now.Add(-15 * time.Minute), Temperature: 17, FeelsLike: 16, Code: 3, IsDay: true, CloudCover: 82, Wind: 3}}
	for i := 0; i < 48; i++ {
		f.Hourly = append(f.Hourly, Sample{Time: mid.Add(time.Duration(i) * time.Hour).UTC(), Temperature: 17, FeelsLike: 17, Code: 3, IsDay: true, RainChance: &rain})
	}
	for i := 0; i < 2; i++ {
		day := mid.AddDate(0, 0, i)
		f.Daily = append(f.Daily, Daily{Date: day.Format("2006-01-02"), Low: 10, High: 20, Code: 3, Sunrise: day.Add(7 * time.Hour).UTC(), Sunset: day.Add(18 * time.Hour).UTC(), RainChance: &rain})
	}
	return f
}
func rawFixture(now time.Time) []byte {
	f := fixture(now)
	cur := map[string]any{"time": f.Current.Time.Unix(), "temperature_2m": 17., "apparent_temperature": 16., "weather_code": 3, "is_day": 1, "cloud_cover": 82, "wind_speed_10m": 3}
	hourly := map[string][]any{}
	daily := map[string][]any{}
	for _, s := range f.Hourly {
		for k, v := range map[string]any{"time": s.Time.Unix(), "temperature_2m": 17., "weather_code": 3, "is_day": 1, "precipitation_probability": 40} {
			hourly[k] = append(hourly[k], v)
		}
	}
	z, _ := time.LoadLocation(f.Location.Zone)
	_, offset := now.In(z).Zone()
	for _, d := range f.Daily {
		day, _ := time.Parse("2006-01-02", d.Date)
		for k, v := range map[string]any{"time": day.Unix() - int64(offset), "temperature_2m_min": 10., "temperature_2m_max": 20., "weather_code": 3, "sunrise": d.Sunrise.Unix(), "sunset": d.Sunset.Unix(), "precipitation_probability_max": 40} {
			daily[k] = append(daily[k], v)
		}
	}
	p := map[string]any{"timezone": f.Location.Zone, "utc_offset_seconds": offset, "current": cur, "hourly": hourly, "daily": daily, "current_units": map[string]string{"time": "unixtime", "temperature_2m": "°C", "apparent_temperature": "°C", "weather_code": "wmo code", "is_day": "", "cloud_cover": "%", "wind_speed_10m": "m/s"}, "hourly_units": map[string]string{"time": "unixtime", "temperature_2m": "°C", "weather_code": "wmo code", "is_day": "", "precipitation_probability": "%"}, "daily_units": map[string]string{"time": "unixtime", "temperature_2m_max": "°C", "temperature_2m_min": "°C", "weather_code": "wmo code", "sunrise": "unixtime", "sunset": "unixtime", "precipitation_probability_max": "%"}}
	raw, _ := json.Marshal(p)
	return raw
}
func TestProviderValidationAndDSTInstants(t *testing.T) {
	now := time.Date(2026, 10, 13, 16, 0, 0, 0, time.UTC)
	l := DefaultSettings().Location
	raw := rawFixture(now)
	f, err := Parse(raw, l, now)
	if err != nil || len(f.Hourly) != 48 || f.Daily[0].Date != "2026-10-13" {
		t.Fatal(f, err)
	}
	cases := map[string]func(map[string]any){"wrong units": func(p map[string]any) { p["current_units"].(map[string]any)["temperature_2m"] = "°F" }, "old current": func(p map[string]any) { p["current"].(map[string]any)["time"] = now.Add(-time.Hour).Unix() }, "missing current": func(p map[string]any) { delete(p, "current") }, "null temperature": func(p map[string]any) { p["current"].(map[string]any)["temperature_2m"] = nil }, "fractional code": func(p map[string]any) { p["current"].(map[string]any)["weather_code"] = 3.5 }, "wrong timezone": func(p map[string]any) { p["timezone"] = "UTC" }, "unequal arrays": func(p map[string]any) { p["hourly"].(map[string]any)["temperature_2m"] = []any{17} }, "unordered hours": func(p map[string]any) { x := p["hourly"].(map[string]any)["time"].([]any); x[1] = x[0] }, "invalid probability": func(p map[string]any) { p["daily"].(map[string]any)["precipitation_probability_max"].([]any)[0] = 101 }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var p map[string]any
			json.Unmarshal(raw, &p)
			change(p)
			bad, _ := json.Marshal(p)
			if _, err := Parse(bad, l, now); err == nil {
				t.Fatal("accepted invalid provider payload")
			}
		})
	}
	z, _ := time.LoadLocation(l.Zone)
	first := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	if first.In(z).Hour() != 1 || second.In(z).Hour() != 1 || !second.After(first) {
		t.Fatal("UTC instants lost repeated DST hour")
	}
	dstNow := time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC)
	dst, err := Parse(rawFixture(dstNow), l, dstNow)
	if err != nil || dst.Hourly[1].Time.In(z).Hour() != 1 || dst.Hourly[2].Time.In(z).Hour() != 1 || !dst.Hourly[2].Time.After(dst.Hourly[1].Time) {
		t.Fatal("forecast parser lost repeated DST hour", err)
	}
	u, _ := ForecastURL(l)
	v, _ := url.Parse(u)
	if v.Query().Get("timeformat") != "unixtime" || v.Query().Get("forecast_days") != "7" || v.Query().Get("temperature_unit") != "celsius" {
		t.Fatal(u)
	}
}
func TestWeatherViewFreshnessAndScenePreview(t *testing.T) {
	cfg := DefaultSettings()
	now := time.Date(2026, 10, 13, 16, 0, 0, 0, time.UTC)
	f := fixture(now)
	v := MakeView(cfg, &f, now, false, false)
	if !v.Available || v.Stale || v.Scene != "cloudy" || v.Temperature != "63°" || v.Rain != "40%" {
		t.Fatal(v)
	}
	cfg.Units = "C"
	cfg.Background = "storm"
	v = MakeView(cfg, &f, now, false, false)
	if v.Temperature != "17°" || v.Scene != "storm" || v.Condition != "Overcast" || v.Preview != "storm" {
		t.Fatal("preview changed readings", v)
	}
	cfg.Background = "live"
	v = MakeView(cfg, &f, now.Add(40*time.Minute), false, true)
	if !v.Available || !v.Stale {
		t.Fatal(v)
	}
	v = MakeView(cfg, &f, now.Add(3*time.Hour), false, true)
	if v.Available || v.Scene != "neutral" || v.Temperature != "" {
		t.Fatal("expired weather looked current", v)
	}
	cfg.Location.ZIP = "10002"
	v = MakeView(cfg, &f, now, true, false)
	if v.Available || v.Scene != "neutral" {
		t.Fatal("old ZIP data leaked")
	}
	cfg = DefaultSettings()
	cfg.Enabled = false
	cfg.Background = "storm"
	v = MakeView(cfg, &f, now, false, false)
	if v.Available || v.Scene != "neutral" {
		t.Fatal(v)
	}
	if IsDay(time.Date(2026, 10, 13, 4, 0, 0, 0, time.UTC), 40.71427, -74.00597) || !IsDay(now, 40.71427, -74.00597) {
		t.Fatal("solar day/night incorrect")
	}
}

type memoryRepo struct {
	cfg   Settings
	f     *Forecast
	saved int
}

func (r *memoryRepo) WeatherSettings() (Settings, error)             { return r.cfg, nil }
func (r *memoryRepo) LoadWeatherCache(l Location) (*Forecast, error) { return r.f, nil }
func (r *memoryRepo) SaveWeatherCache(f Forecast) error              { r.f = &f; r.saved++; return nil }

type fakeProvider struct {
	calls int
	fail  bool
}

func (p *fakeProvider) ResolveZIP(context.Context, string) (Location, error) {
	return Location{}, errors.New("offline")
}
func (p *fakeProvider) Fetch(ctx context.Context, l Location, now time.Time) (Forecast, error) {
	p.calls++
	if p.fail {
		return Forecast{}, errors.New("offline")
	}
	f := fixture(now)
	f.Location = l
	return f, nil
}
func TestCacheRefreshBackoffAndRestart(t *testing.T) {
	now := time.Date(2026, 10, 13, 16, 0, 0, 0, time.UTC)
	r := &memoryRepo{cfg: DefaultSettings()}
	p := &fakeProvider{}
	s := NewService(r, p)
	s.Refresh(context.Background(), now)
	for i := 0; i < 10; i++ {
		s.View(r.cfg, now)
		s.Refresh(context.Background(), now.Add(time.Minute))
	}
	if p.calls != 1 || r.saved != 1 {
		t.Fatal("page loads caused provider requests", p.calls)
	}
	restarted := NewService(r, p)
	restarted.Refresh(context.Background(), now.Add(time.Minute))
	if p.calls != 1 || !restarted.View(r.cfg, now.Add(time.Minute)).Available {
		t.Fatal("fresh persistent cache not reused")
	}
	p.fail = true
	s.Refresh(context.Background(), now.Add(Refresh))
	s.Refresh(context.Background(), now.Add(Refresh+30*time.Second))
	if p.calls != 2 {
		t.Fatal("failure not throttled")
	}
	if !s.View(r.cfg, now.Add(Refresh)).Stale {
		t.Fatal("failed refresh not labeled stale")
	}
	r.cfg.Location.ZIP = "10002"
	if s.View(r.cfg, now).Available {
		t.Fatal("old location appeared under new ZIP")
	}
	r.cfg.Enabled = false
	s.Refresh(context.Background(), now.Add(time.Hour))
	if p.calls != 2 {
		t.Fatal("disabled weather fetched")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestZIPMatchingTransportAndBounds(t *testing.T) {
	response := `{"results":[{"name":"New York","admin1":"New York","country_code":"US","timezone":"America/New_York","latitude":40.71427,"longitude":-74.00597,"postcodes":["10001"]}]}`
	c := NewClient()
	c.HTTP.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "geocoding-api.open-meteo.com" || r.URL.Query().Get("name") != "10001" || r.URL.Query().Get("countryCode") != "US" {
			t.Fatal(r.URL)
		}
		w := httptest.NewRecorder()
		w.WriteString(response)
		return w.Result(), nil
	})
	l, err := c.ResolveZIP(context.Background(), "10001")
	if err != nil || l.ZIP != "10001" || l.Zone != "America/New_York" {
		t.Fatal(l, err)
	}
	response = `{"results":[{"name":"Wrong city","country_code":"US","timezone":"America/New_York","latitude":42,"longitude":-71,"postcodes":["10002"]}]}`
	if _, err = c.ResolveZIP(context.Background(), "10001"); err == nil {
		t.Fatal("nonmatching postcode accepted")
	}
	if err = c.get(context.Background(), "http://192.168.1.1/", &l, 100); err == nil {
		t.Fatal("arbitrary endpoint allowed")
	}
	response = "123456"
	if err = c.get(context.Background(), "https://geocoding-api.open-meteo.com/v1/search?name=10001&countryCode=US", &l, 2); err == nil {
		t.Fatal("oversized response accepted")
	}
	if !reflect.DeepEqual(ConditionPair(97), []string{"storm", "Thunderstorms"}) {
		t.Fatal("heavy storm code not mapped")
	}
}
func ConditionPair(code int) []string { k, l := Condition(code); return []string{k, l} }

func TestAppearanceFollowsSunIndependentlyOfWeather(t *testing.T) {
	cfg := DefaultSettings()
	day := time.Date(2026, 10, 13, 16, 0, 0, 0, time.UTC)
	night := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	for _, enabled := range []bool{true, false} {
		cfg.Enabled = enabled
		cfg.Background = "sunny"
		v := MakeView(cfg, nil, night, false, true)
		if v.Theme != "dark" {
			t.Fatal("sunny preview/unavailable weather suppressed night", v)
		}
		if Appearance(cfg, day) != "light" {
			t.Fatal("day not light")
		}
	}
	cfg.Theme = "light"
	if Appearance(cfg, night) != "light" {
		t.Fatal("light override ignored")
	}
	cfg.Theme = "dark"
	if Appearance(cfg, day) != "dark" {
		t.Fatal("dark override ignored")
	}
	cfg.Theme = "invalid"
	if cfg.Validate() == nil {
		t.Fatal("unknown appearance accepted")
	}
	cfg.Theme = "auto"
	if Appearance(cfg, day) != Appearance(cfg, day.In(time.FixedZone("different", 9*3600))) {
		t.Fatal("appearance depended on host time zone")
	}
}

func TestPrecipitationLabelsFollowHomeDayForecast(t *testing.T) {
	// UTC has rolled into October 14, but the home forecast day is October 13.
	now := time.Date(2026, 10, 14, 2, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                                 string
		daily, firstHour, nextHour, tomorrow int
		want                                 string
	}{
		{"warm cloudy day", 3, 3, 3, 75, "Rain chance"},
		{"rain", 61, 61, 63, 75, "Rain chance"},
		{"drizzle", 51, 53, 55, 3, "Rain chance"},
		{"thunderstorms", 95, 95, 96, 3, "Rain chance"},
		{"freezing rain", 66, 56, 67, 3, "Rain chance"},
		{"snow", 71, 73, 85, 61, "Snow chance"},
		{"daily snow with cloudy hours", 75, 3, 3, 61, "Snow chance"},
		{"hourly snow with cloudy daily", 3, 71, 85, 61, "Snow chance"},
		{"rain turning to snow", 75, 61, 71, 3, "Rain / snow chance"},
		{"snow turning to rain", 61, 71, 61, 3, "Rain / snow chance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture(now)
			chance := 12.
			f.Current.Temperature = 24 // 75 F now does not override later precipitation.
			f.Daily[0].Code, f.Daily[0].RainChance = tc.daily, &chance
			f.Daily[1].Code = tc.tomorrow
			for i := range f.Hourly {
				f.Hourly[i].Code = 3
				if i >= 24 {
					f.Hourly[i].Code = tc.tomorrow
				}
			}
			f.Hourly[8].Code, f.Hourly[20].Code = tc.firstHour, tc.nextHour
			cfg := DefaultSettings()
			cfg.Background = "snow" // Visual previews must not change the label.
			v := MakeView(cfg, &f, now, false, false)
			if !v.Available || v.PrecipitationLabel != tc.want || v.Rain != "12%" {
				t.Fatalf("got label %q, probability %q, available %v", v.PrecipitationLabel, v.Rain, v.Available)
			}
			f.Daily[0].RainChance = nil
			v = MakeView(cfg, &f, now, false, false)
			if v.Rain != "" || v.PrecipitationLabel != "" {
				t.Fatal("missing probability must not appear as a chance", v)
			}
		})
	}
}

func TestAtmosphereUsesLiveConditionsAndHomeSun(t *testing.T) {
	for _, now := range []time.Time{
		time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 14, 4, 0, 0, 0, time.UTC),
	} {
		for code, want := range map[int]string{0: "clear", 2: "partly-cloudy", 3: "cloudy", 61: "rain", 95: "storm", 75: "snow", 45: "fog"} {
			cfg := DefaultSettings()
			cfg.Background = "sunny"
			cfg.Theme = "light"
			f := fixture(now)
			f.Current.Code = code
			v := MakeView(cfg, &f, now, false, false)
			if !v.Available || v.Atmosphere != want || v.Daylight != (now.Hour() == 16) {
				t.Fatalf("code %d at %s: atmosphere %q daylight %v", code, now, v.Atmosphere, v.Daylight)
			}
			v = MakeView(cfg, &f, now.Add(3*time.Hour), false, true)
			if v.Available || v.Atmosphere != "" {
				t.Fatal("expired forecast retained a live sky", v)
			}
		}
	}
}
