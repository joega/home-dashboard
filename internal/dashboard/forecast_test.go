package dashboard

import (
	"context"
	"home-dashboard/internal/weather"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type forecastPageProvider struct{ dashboardWeatherProvider }

func (p *forecastPageProvider) Fetch(_ context.Context, l weather.Location, now time.Time) (weather.Forecast, error) {
	p.fetches++
	zone, _ := time.LoadLocation(l.Zone)
	at := now.In(zone)
	mid := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, zone)
	rain := 30.
	f := weather.Forecast{Location: l, RetrievedAt: now, Current: weather.Sample{Time: now, Temperature: 20, FeelsLike: 19, Code: 2, IsDay: true, Wind: 4}}
	for at := mid; at.Before(mid.AddDate(0, 0, 7)); at = at.Add(time.Hour) {
		f.Hourly = append(f.Hourly, weather.Sample{Time: at, Temperature: 20, FeelsLike: 20, Code: 2, IsDay: true, RainChance: &rain})
	}
	for i := 0; i < 7; i++ {
		d := mid.AddDate(0, 0, i)
		f.Daily = append(f.Daily, weather.Daily{Date: d.Format("2006-01-02"), High: 22, Low: 10, Code: 2, Sunrise: d.Add(7 * time.Hour), Sunset: d.Add(18 * time.Hour), RainChance: &rain})
	}
	return f, nil
}
func TestForecastPageLinksDayNavigationAndCacheOnlyReads(t *testing.T) {
	s, _ := testStore(t)
	provider := &forecastPageProvider{}
	zone, _ := time.LoadLocation("America/New_York")
	a, err := New(s, zone, provider)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	a.weather.Refresh(context.Background(), now)
	get := func(path string, hx bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://household.test"+path, nil)
		if hx {
			r.Header.Set("HX-Request", "true")
			r.Header.Set("HX-Current-URL", "http://household.test/")
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		return w
	}
	today := get("/", false).Body.String()
	if !strings.Contains(today, `class="weather-strip-link" href="/weather"`) || !strings.Contains(today, `aria-label="View hourly and 7-day weather forecast"`) {
		t.Fatal("Today strip is not a forecast link")
	}
	full := get("/weather", false).Body.String()
	for _, want := range []string{"<title>Home HQ · Weather</title>", `class="nav-item selected" aria-current="page">Weather</a>`, "Next 24 hours", "7-day forecast", "Wind now", `aria-label="Weather settings"`, `data-forecast-scroll=`} {
		if !strings.Contains(full, want) {
			t.Fatal("missing forecast detail", want)
		}
	}
	date := now.In(zone).AddDate(0, 0, 2).Format("2006-01-02")
	fragment := get("/weather?date="+date, true)
	if strings.Contains(fragment.Body.String(), "<!doctype") || !strings.Contains(fragment.Body.String(), `href="/weather?date=`+date+`" hx-boost="true" hx-target="#content" aria-current="date"`) || fragment.Header().Get("HX-Push-Url") != "/weather?date="+date {
		t.Fatal("day selection did not render/push selected date")
	}
	if !strings.Contains(get("/weather?date=2099-01-01", false).Body.String(), "Next 24 hours") {
		t.Fatal("out-of-range date has no safe fallback")
	}
	for i := 0; i < 3; i++ {
		get("/weather", true)
	}
	if provider.fetches != 1 || provider.resolves != 0 {
		t.Fatal("page reads fetched external data", provider.fetches, provider.resolves)
	}
	cfg, _ := s.WeatherSettings()
	cfg.Enabled = false
	if err = s.SaveWeatherSettings(cfg); err != nil {
		t.Fatal(err)
	}
	disabled := get("/weather", false).Body.String()
	if !strings.Contains(disabled, "Weather is off") || strings.Contains(disabled, `class="forecast-hours"`) || !strings.Contains(disabled, ">Weather settings ") {
		t.Fatal("disabled state is incomplete")
	}
}
func TestForecastPageLoadingAndExpiredData(t *testing.T) {
	s, _ := testStore(t)
	a, err := New(s, time.UTC, &forecastPageProvider{})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.WeatherSettings()
	for _, tc := range []struct {
		name     string
		forecast *weather.Forecast
		status   string
	}{{"loading", nil, "Loading weather"}, {"expired", func() *weather.Forecast {
		p := &forecastPageProvider{}
		f, _ := p.Fetch(context.Background(), cfg.Location, time.Now().Add(-3*time.Hour))
		return &f
	}(), "Weather unavailable"}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.forecast != nil {
				raw := tc.forecast // An expired snapshot must not expose any hours.
				// Render an expired snapshot directly to exercise the unavailable template.
				p := a.base(httptest.NewRecorder(), httptest.NewRequest("GET", "http://household.test/weather", nil))
				p.View = "weather"
				p.Title = "Weather"
				p.Forecast = weather.MakeForecastView(cfg, raw, p.Now, "", false, true)
				p.Weather = p.Forecast.Current
				w := httptest.NewRecorder()
				a.render(w, "page", p)
				if w.Code != 200 || !strings.Contains(w.Body.String(), tc.status) || strings.Contains(w.Body.String(), `class="forecast-hours"`) {
					t.Fatal("expired data remained visible")
				}
				return
			}
			w := httptest.NewRecorder()
			a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/weather", nil))
			if w.Code != 200 || !strings.Contains(w.Body.String(), tc.status) || strings.Contains(w.Body.String(), `class="forecast-hours"`) {
				t.Fatal("loading template failed")
			}
		})
	}
}
