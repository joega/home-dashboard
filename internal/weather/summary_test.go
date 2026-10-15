package weather

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWeatherSummaryCoveragePrecipitationAndMissingMetrics(t *testing.T) {
	cfg := DefaultSettings()
	now := time.Date(2026, 10, 15, 14, 30, 0, 0, time.UTC)
	f := fullForecast(now)
	v := MakeForecastView(cfg, &f, now, "", false, false)
	if len(v.HomeHourly) != 6 || v.Summary != "Mostly dry for the next 12 hours" || v.Gusts != "—" || v.Humidity != "—" || v.UV != "—" || v.TemperaturePoints == "" {
		t.Fatal(v)
	}
	seventy := 70.
	humidity := 80.
	gust := 15.
	direction := 225.
	uv := 4.5
	f.Current.Humidity = &humidity
	f.Current.WindGust = &gust
	f.Current.WindDirection = &direction
	f.Daily[0].UV = &uv
	for i := range f.Hourly {
		if f.Hourly[i].Time.Equal(now.Truncate(time.Hour).Add(2 * time.Hour)) {
			f.Hourly[i].RainChance = &seventy
			f.Hourly[i].Code = 71
		}
	}
	v = MakeForecastView(cfg, &f, now, "", false, false)
	if !strings.HasPrefix(v.Summary, "Snow likely around ") || v.Gusts != "34 mph" || v.Humidity != "80%" || v.UV != "4.5" || !strings.HasPrefix(v.Wind, "SW ") {
		t.Fatal(v)
	}
	cfg.Units = "C"
	v = MakeForecastView(cfg, &f, now, "", false, false)
	if v.Gusts != "54 km/h" {
		t.Fatal(v.Gusts)
	}
	f = fullForecast(now)
	f.Hourly = append(f.Hourly[:15], f.Hourly[16:]...)
	v = MakeForecastView(cfg, &f, now, "", false, false)
	if strings.Contains(v.Summary, "dry") {
		t.Fatal("missing hour implied complete dry window")
	}
	f = fullForecast(now)
	for i := range f.Hourly {
		f.Hourly[i].RainChance = nil
	}
	v = MakeForecastView(cfg, &f, now, "", false, false)
	if strings.Contains(v.Summary, "dry") {
		t.Fatal("unknown probability promised dry weather")
	}
}
func TestOptionalProviderMetricsAndOldCacheCompatibility(t *testing.T) {
	now := time.Now()
	var raw map[string]any
	if err := json.Unmarshal(rawFixture(now), &raw); err != nil {
		t.Fatal(err)
	}
	c := raw["current"].(map[string]any)
	c["relative_humidity_2m"] = 72.
	c["wind_gusts_10m"] = 13.
	c["wind_direction_10m"] = 300.
	d := raw["daily"].(map[string]any)
	uv := make([]any, len(d["time"].([]any)))
	for i := range uv {
		uv[i] = 3.2
	}
	d["uv_index_max"] = uv
	bytes, _ := json.Marshal(raw)
	f, err := Parse(bytes, DefaultSettings().Location, now)
	if err != nil || f.Current.Humidity == nil || *f.Current.Humidity != 72 || f.Daily[0].UV == nil {
		t.Fatal(f, err)
	}
	// The optional additions must not invalidate a working older cached forecast.
	old, err := Parse(rawFixture(now), DefaultSettings().Location, now)
	if err != nil || old.Current.Humidity != nil || old.Daily[0].UV != nil {
		t.Fatal(err)
	}
	c["relative_humidity_2m"] = 101.
	bytes, _ = json.Marshal(raw)
	if _, err = Parse(bytes, DefaultSettings().Location, now); err == nil {
		t.Fatal("invalid humidity accepted")
	}
	delete(c, "relative_humidity_2m")
	d["uv_index_max"] = []any{}
	bytes, _ = json.Marshal(raw)
	if _, err = Parse(bytes, DefaultSettings().Location, now); err == nil {
		t.Fatal("mismatched UV accepted")
	}
}
