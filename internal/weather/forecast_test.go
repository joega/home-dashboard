package weather

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fullForecast(now time.Time) Forecast {
	cfg := DefaultSettings()
	zone, _ := time.LoadLocation(cfg.Location.Zone)
	local := now.In(zone)
	mid := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, zone)
	zero := 0.
	f := Forecast{Location: cfg.Location, RetrievedAt: now, Current: Sample{Time: now, Temperature: 20, FeelsLike: 19, Code: 2, IsDay: true, Wind: 4}}
	for at := mid; at.Before(mid.AddDate(0, 0, 7)); at = at.Add(time.Hour) {
		f.Hourly = append(f.Hourly, Sample{Time: at, Temperature: 20, FeelsLike: 20, Code: 0, IsDay: at.Hour() >= 7 && at.Hour() < 18, RainChance: &zero})
	}
	for i := 0; i < 7; i++ {
		at := mid.AddDate(0, 0, i)
		f.Daily = append(f.Daily, Daily{Date: at.Format("2006-01-02"), Low: 10, High: 22, Code: 0, Sunrise: time.Date(at.Year(), at.Month(), at.Day(), 7, 0, 0, 0, zone), Sunset: time.Date(at.Year(), at.Month(), at.Day(), 18, 0, 0, 0, zone), RainChance: &zero})
	}
	return f
}
func TestForecastViewCoverageSelectionUnitsAndSafety(t *testing.T) {
	cfg := DefaultSettings()
	now := time.Date(2026, 10, 14, 13, 40, 0, 0, time.UTC)
	f := fullForecast(now)
	if err := f.Validate(now); err != nil {
		t.Fatal(err)
	}
	v := MakeForecastView(cfg, &f, now, "", false, false)
	if !v.Current.Available || len(v.Days) != 7 || len(v.Hourly) != 24 || v.Hourly[0].Label != "Now" || v.Hourly[0].Temperature != "68°" || v.Wind != "9 mph" || v.Days[0].Rain != "0%" || !v.Days[0].Selected || v.Sunrise != "7:00 AM" {
		t.Fatal(v)
	}
	if v.Hourly[23].Date == v.Hourly[0].Date {
		t.Fatal("rolling forecast must include tomorrow")
	}
	requested := f.Daily[2].Date
	v = MakeForecastView(cfg, &f, now, requested, false, false)
	if v.SelectedDate != requested || len(v.Hourly) != 24 || v.Days[0].Selected || !v.Days[2].Selected {
		t.Fatal(v)
	}
	for _, h := range v.Hourly {
		at, _ := time.Parse(time.RFC3339, h.Time)
		if at.In(now.Location()).Before(now) {
			t.Fatal("past forecast leaked", h)
		}
	}
	if v.Hourly[0].Icon != "night" {
		t.Fatal("night hour depicted as sunshine", v.Hourly[0])
	}
	cfg.Units = "C"
	cfg.Background = "snow"
	v = MakeForecastView(cfg, &f, now, requested, false, false)
	if v.Hourly[0].Temperature != "20°" || v.Wind != "14 km/h" || v.Days[0].Icon != "clear" || v.Current.Scene != "snow" {
		t.Fatal("preview or units changed forecast", v)
	}
	for _, bad := range []string{"garbage", "2026-02-30", "2099-01-01", "<script>"} {
		v = MakeForecastView(cfg, &f, now, bad, false, false)
		if v.SelectedDate != "" || v.HourlyTitle != "Next 24 hours" || len(v.Hourly) != 24 {
			t.Fatal("invalid date did not fall back", v)
		}
	}
	f.Daily = f.Daily[:2]
	f.Hourly[10].RainChance = nil
	v = MakeForecastView(cfg, &f, now, "", false, true)
	if !v.Current.Stale || v.Coverage != "2 of 7 days available" || v.Hourly[1].Rain != "" {
		t.Fatal("partial/missing forecast mislabeled", v)
	}
	short := f
	short.Hourly = short.Hourly[:10]
	limited := MakeForecastView(cfg, &short, now, "", false, false)
	if limited.HourlyCoverage != "1 of 24 hours available" {
		t.Fatal("limited hourly coverage is not labeled", limited)
	}
	gapped := fullForecast(now)
	gapped.Hourly = append(gapped.Hourly[:10], gapped.Hourly[11:]...)
	gapView := MakeForecastView(cfg, &gapped, now, "", false, false)
	if len(gapView.Hourly) != 23 || gapView.HourlyCoverage != "23 of 24 hours available" {
		t.Fatal("missing hour extended the forecast window", gapView)
	}
	cfg.Enabled = false
	v = MakeForecastView(cfg, &f, now, "", false, false)
	if v.Current.Available || len(v.Days) > 0 || len(v.Hourly) > 0 {
		t.Fatal("disabled weather exposed forecast")
	}
	cfg.Enabled = true
	for _, at := range []time.Time{now.Add(Expire + time.Second), now.Add(-time.Second)} {
		v = MakeForecastView(cfg, &f, at, "", false, false)
		if v.Current.Available || len(v.Days) > 0 || len(v.Hourly) > 0 {
			t.Fatal("expired or future cache exposed forecast")
		}
	}
	cfg.Location.ZIP = "10002"
	v = MakeForecastView(cfg, &f, now, "", false, false)
	if v.Current.Available || len(v.Days) > 0 || len(v.Hourly) > 0 {
		t.Fatal("old location exposed forecast")
	}
}
func TestForecastViewPrecipitationAndLocalMidnight(t *testing.T) {
	cfg := DefaultSettings()
	now := time.Date(2026, 10, 15, 2, 30, 0, 0, time.UTC) // still Oct 14 at home
	f := fullForecast(now)
	f.Daily[1].Code = 71
	for i := range f.Hourly {
		if f.Hourly[i].Time.Format("2006-01-02") == f.Daily[1].Date {
			f.Hourly[i].Code = 71
		}
	}
	v := MakeForecastView(cfg, &f, now, "", false, false)
	if v.Days[0].Date != "2026-10-14" || v.Days[1].Label != "Tomorrow" || v.Days[1].PrecipitationLabel != "Snow chance" {
		t.Fatal(v.Days)
	}
	f.Hourly[26].Code = 61
	v = MakeForecastView(cfg, &f, now, f.Daily[1].Date, false, false)
	if v.Days[1].PrecipitationLabel != "Rain / snow chance" || v.Hourly[0].PrecipitationLabel != "Snow chance" {
		t.Fatal("mixed daily and individual hourly types incorrect", v)
	}
}
func TestForecastDSTDaysRetainAllInstants(t *testing.T) {
	cfg := DefaultSettings()
	for _, tc := range []struct {
		now   time.Time
		date  string
		hours int
	}{{time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC), "2026-11-01", 25}, {time.Date(2026, 3, 7, 16, 0, 0, 0, time.UTC), "2026-03-08", 23}} {
		f := fullForecast(tc.now)
		v := MakeForecastView(cfg, &f, tc.now, tc.date, false, false)
		if len(v.Hourly) != tc.hours {
			t.Fatalf("%s: got %d hours", tc.date, len(v.Hourly))
		}
		labels := map[string]bool{}
		for _, h := range v.Hourly {
			if labels[h.Label] {
				t.Fatal("ambiguous repeated hour", h)
			}
			labels[h.Label] = true
		}
		if tc.hours == 25 && (!labels["1 AM EDT"] || !labels["1 AM EST"]) {
			t.Fatal(labels)
		}
	}
}
func TestProviderDailyDatesUseFixedProviderOffsetAcrossDST(t *testing.T) {
	now := time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC)
	// Open-Meteo daily timestamps are fixed-offset date markers, unlike hourly instants.
	var raw map[string]any
	if err := json.Unmarshal(rawFixture(now), &raw); err != nil {
		t.Fatal(err)
	}
	f := fullForecast(now)
	daily := raw["daily"].(map[string]any)
	times, lows, highs, codes, rises, sets, chances := []any{}, []any{}, []any{}, []any{}, []any{}, []any{}, []any{}
	for _, d := range f.Daily {
		at, _ := time.Parse("2006-01-02", d.Date)
		times = append(times, at.Unix()+4*60*60)
		lows = append(lows, 10)
		highs = append(highs, 22)
		codes = append(codes, 0)
		rises = append(rises, d.Sunrise.Unix())
		sets = append(sets, d.Sunset.Unix())
		chances = append(chances, 0)
	}
	for k, v := range map[string][]any{"time": times, "temperature_2m_min": lows, "temperature_2m_max": highs, "weather_code": codes, "sunrise": rises, "sunset": sets, "precipitation_probability_max": chances} {
		daily[k] = v
	}
	// Keep the response offset across fall-back: Nov 2's marker is still 04:00 UTC.
	raw["utc_offset_seconds"] = -14400
	bytes, _ := json.Marshal(raw)
	parsed, err := Parse(bytes, f.Location, now)
	if err != nil || len(parsed.Daily) != 7 || parsed.Daily[0].Date != "2026-10-31" || parsed.Daily[2].Date != "2026-11-02" {
		t.Fatal(parsed.Daily, err)
	}
	if strings.Contains(MakeForecastView(DefaultSettings(), &parsed, now, "", false, false).Coverage, "of 7") {
		t.Fatal("provider offset shifted daily coverage")
	}
}
