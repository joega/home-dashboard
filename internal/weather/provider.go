package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Provider interface {
	ResolveZIP(context.Context, string) (Location, error)
	Fetch(context.Context, Location, time.Time) (Forecast, error)
}
type Client struct{ HTTP *http.Client }

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("weather redirects refused") }}}
}
func (c *Client) get(ctx context.Context, raw string, dst any, limit int64) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.User != nil || (u.Host != "api.open-meteo.com" && u.Host != "geocoding-api.open-meteo.com") {
		return errors.New("unsupported weather endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Home-HQ/1.0 (household dashboard)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("weather provider HTTP %d", resp.StatusCode)
	}
	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(rawBody)) > limit {
		return errors.New("weather response too large")
	}
	return json.Unmarshal(rawBody, dst)
}
func (c *Client) ResolveZIP(ctx context.Context, zip string) (Location, error) {
	if !ValidZIP(zip) {
		return Location{}, errors.New("Enter a five-digit US ZIP code.")
	}
	q := url.Values{"name": {zip}, "countryCode": {"US"}, "count": {"10"}, "language": {"en"}, "format": {"json"}}
	var payload struct {
		Error   bool
		Results []struct {
			Name        string   `json:"name"`
			Admin1      string   `json:"admin1"`
			CountryCode string   `json:"country_code"`
			Timezone    string   `json:"timezone"`
			Latitude    *float64 `json:"latitude"`
			Longitude   *float64 `json:"longitude"`
			Postcodes   []string `json:"postcodes"`
		}
	}
	if err := c.get(ctx, "https://geocoding-api.open-meteo.com/v1/search?"+q.Encode(), &payload, 128<<10); err != nil {
		return Location{}, errors.New("Couldn't look up that ZIP. Try again when weather is reachable.")
	}
	if payload.Error || len(payload.Results) >= 10 {
		return Location{}, errors.New("Couldn't uniquely resolve that ZIP.")
	}
	var matches []Location
	for _, r := range payload.Results {
		if r.CountryCode != "US" {
			continue
		}
		for _, code := range r.Postcodes {
			if code == zip && r.Latitude != nil && r.Longitude != nil {
				l := Location{Name: r.Name + ", " + r.Admin1, ZIP: zip, Zone: r.Timezone, Latitude: *r.Latitude, Longitude: *r.Longitude}
				if l.Validate() != nil {
					return Location{}, errors.New("Invalid location returned by weather provider.")
				}
				matches = append(matches, l)
				break
			}
		}
	}
	if len(matches) != 1 {
		return Location{}, errors.New("ZIP not found or ambiguous. Keep the existing location and try another ZIP.")
	}
	return matches[0], nil
}
func ForecastURL(l Location) (string, error) {
	if err := l.Validate(); err != nil {
		return "", err
	}
	q := url.Values{"latitude": {strconv.FormatFloat(l.Latitude, 'f', 5, 64)}, "longitude": {strconv.FormatFloat(l.Longitude, 'f', 5, 64)}, "timezone": {l.Zone}, "timeformat": {"unixtime"}, "temperature_unit": {"celsius"}, "wind_speed_unit": {"ms"}, "forecast_days": {"7"}, "current": {"temperature_2m,apparent_temperature,weather_code,is_day,cloud_cover,wind_speed_10m"}, "hourly": {"temperature_2m,weather_code,is_day,precipitation_probability"}, "daily": {"temperature_2m_max,temperature_2m_min,weather_code,sunrise,sunset,precipitation_probability_max"}}
	return "https://api.open-meteo.com/v1/forecast?" + q.Encode(), nil
}
func (c *Client) Fetch(ctx context.Context, l Location, now time.Time) (Forecast, error) {
	rawURL, err := ForecastURL(l)
	if err != nil {
		return Forecast{}, err
	}
	var raw json.RawMessage
	if err = c.get(ctx, rawURL, &raw, 512<<10); err != nil {
		return Forecast{}, err
	}
	return Parse(raw, l, now)
}
func Parse(raw []byte, l Location, now time.Time) (Forecast, error) {
	var p struct {
		Error        bool
		Timezone     string
		Offset       int64             `json:"utc_offset_seconds"`
		CurrentUnits map[string]string `json:"current_units"`
		HourlyUnits  map[string]string `json:"hourly_units"`
		DailyUnits   map[string]string `json:"daily_units"`
		Current      map[string]*float64
		Hourly       map[string][]*float64
		Daily        map[string][]*float64
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Forecast{}, err
	}
	if p.Error || p.Timezone != l.Zone || p.Offset < -86400 || p.Offset > 86400 {
		return Forecast{}, errors.New("invalid provider location")
	}
	units := func(got map[string]string, want string) error {
		for _, pair := range strings.Split(want, ";") {
			parts := strings.SplitN(pair, "=", 2)
			if v, ok := got[parts[0]]; !ok || v != parts[1] {
				return errors.New("unexpected weather units")
			}
		}
		return nil
	}
	if err := units(p.CurrentUnits, "time=unixtime;temperature_2m=°C;apparent_temperature=°C;weather_code=wmo code;is_day=;cloud_cover=%;wind_speed_10m=m/s"); err != nil {
		return Forecast{}, err
	}
	if err := units(p.HourlyUnits, "time=unixtime;temperature_2m=°C;weather_code=wmo code;is_day=;precipitation_probability=%"); err != nil {
		return Forecast{}, err
	}
	if err := units(p.DailyUnits, "time=unixtime;temperature_2m_max=°C;temperature_2m_min=°C;weather_code=wmo code;sunrise=unixtime;sunset=unixtime;precipitation_probability_max=%"); err != nil {
		return Forecast{}, err
	}
	number := func(m map[string]*float64, k string) (float64, error) {
		v := m[k]
		if v == nil {
			return 0, fmt.Errorf("missing %s", k)
		}
		return *v, nil
	}
	stamp := func(v *float64) (time.Time, error) {
		if v == nil || !finite(*v, 946684800, 4102444800) || *v != float64(int64(*v)) {
			return time.Time{}, errors.New("invalid weather time")
		}
		return time.Unix(int64(*v), 0).UTC(), nil
	}
	sample := func(m map[string]*float64, current bool) (Sample, error) {
		var s Sample
		var err error
		s.Time, err = stamp(m["time"])
		if err != nil {
			return s, err
		}
		s.Temperature, err = number(m, "temperature_2m")
		if err != nil {
			return s, err
		}
		code, e := number(m, "weather_code")
		if e != nil || !finite(code, 0, 999) || code != float64(int(code)) {
			return s, errors.New("invalid weather code")
		}
		s.Code = int(code)
		day, e := number(m, "is_day")
		if e != nil || (day != 0 && day != 1) {
			return s, errors.New("invalid day/night")
		}
		s.IsDay = day == 1
		s.FeelsLike = s.Temperature
		if current {
			for k, dst := range map[string]*float64{"apparent_temperature": &s.FeelsLike, "cloud_cover": &s.CloudCover, "wind_speed_10m": &s.Wind} {
				*dst, err = number(m, k)
				if err != nil {
					return s, err
				}
			}
		} else {
			s.RainChance = m["precipitation_probability"]
		}
		if !validSample(s) {
			return s, errors.New("invalid weather values")
		}
		return s, nil
	}
	f := Forecast{Location: l, RetrievedAt: now.UTC()}
	var err error
	f.Current, err = sample(p.Current, true)
	if err != nil {
		return f, err
	}
	rows := func(m map[string][]*float64, keys []string, max int) ([]map[string]*float64, error) {
		n := len(m["time"])
		if n < 1 || n > max {
			return nil, errors.New("invalid forecast length")
		}
		for _, k := range keys {
			if len(m[k]) != n {
				return nil, errors.New("mismatched forecast arrays")
			}
		}
		r := make([]map[string]*float64, n)
		for i := range r {
			r[i] = map[string]*float64{}
			for _, k := range keys {
				r[i][k] = m[k][i]
			}
		}
		return r, nil
	}
	hrs, err := rows(p.Hourly, []string{"time", "temperature_2m", "weather_code", "is_day", "precipitation_probability"}, 400)
	if err != nil {
		return f, err
	}
	for _, r := range hrs {
		s, err := sample(r, false)
		if err != nil {
			return f, err
		}
		f.Hourly = append(f.Hourly, s)
	}
	days, err := rows(p.Daily, []string{"time", "temperature_2m_max", "temperature_2m_min", "weather_code", "sunrise", "sunset", "precipitation_probability_max"}, 16)
	if err != nil {
		return f, err
	}
	for _, r := range days {
		t, e := stamp(r["time"])
		if e != nil {
			return f, e
		}
		// Open-Meteo encodes daily date markers with its fixed utc_offset_seconds.
		// Applying the location's varying DST offset can shift them to the prior date.
		// See https://open-meteo.com/en/docs (timeformat).
		d := Daily{Date: t.Add(time.Duration(p.Offset) * time.Second).Format("2006-01-02"), RainChance: r["precipitation_probability_max"]}
		d.Low, e = number(r, "temperature_2m_min")
		if e != nil {
			return f, e
		}
		d.High, e = number(r, "temperature_2m_max")
		if e != nil {
			return f, e
		}
		code, e := number(r, "weather_code")
		if e != nil || !finite(code, 0, 999) || code != float64(int(code)) {
			return f, errors.New("invalid daily code")
		}
		d.Code = int(code)
		d.Sunrise, e = stamp(r["sunrise"])
		if e != nil {
			return f, e
		}
		d.Sunset, e = stamp(r["sunset"])
		if e != nil {
			return f, e
		}
		f.Daily = append(f.Daily, d)
	}
	return f, f.Validate(now)
}
