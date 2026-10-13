// Package weather adapts provider validation and condition rules from A Weather App.
// See THIRD_PARTY_NOTICES.md for the original MIT license.
package weather

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"
)

const Refresh = 15 * time.Minute
const Expire = 2 * time.Hour

type Location struct {
	Name, ZIP, Zone     string
	Latitude, Longitude float64
}

func (l Location) Key() string {
	return fmt.Sprintf("%s:%.5f:%.5f:%s", l.ZIP, l.Latitude, l.Longitude, l.Zone)
}
func (l Location) Validate() error {
	if !ValidZIP(l.ZIP) || l.Name == "" || len(l.Name) > 240 || !finite(l.Latitude, -90, 90) || !finite(l.Longitude, -180, 180) {
		return errors.New("Invalid weather location.")
	}
	if l.Zone == "Local" || l.Zone == "" {
		return errors.New("Invalid weather time zone.")
	}
	_, err := time.LoadLocation(l.Zone)
	return err
}

var zipPattern = regexp.MustCompile(`^[0-9]{5}$`)

func ValidZIP(s string) bool { return zipPattern.MatchString(s) }

type Settings struct {
	Location                 Location
	Units, Background, Theme string
	Enabled                  bool
}

func DefaultSettings() Settings {
	return Settings{Location: Location{Name: "North Attleboro, MA", ZIP: "02760", Zone: "America/New_York", Latitude: 41.98343, Longitude: -71.33283}, Units: "F", Background: "live", Theme: "auto", Enabled: true}
}
func (s Settings) Validate() error {
	if err := s.Location.Validate(); err != nil {
		return err
	}
	if s.Units != "F" && s.Units != "C" {
		return errors.New("Choose Fahrenheit or Celsius.")
	}
	if s.Theme != "" && s.Theme != "auto" && s.Theme != "light" && s.Theme != "dark" {
		return errors.New("Choose Automatic, Light or Dark appearance.")
	}
	switch s.Background {
	case "live", "off", "sunny", "cloudy", "rain", "storm", "snow", "fog", "night":
	default:
		return errors.New("Choose a background from the list.")
	}
	return nil
}

type Sample struct {
	Time                                     time.Time
	Temperature, FeelsLike, CloudCover, Wind float64
	Code                                     int
	IsDay                                    bool
	RainChance                               *float64
}
type Daily struct {
	Date            string
	Low, High       float64
	Code            int
	Sunrise, Sunset time.Time
	RainChance      *float64
}
type Forecast struct {
	Location    Location
	RetrievedAt time.Time
	Current     Sample
	Hourly      []Sample
	Daily       []Daily
}

func finite(v, lo, hi float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi }
func Condition(code int) (kind, label string) {
	switch code {
	case 0:
		return "clear", "Clear"
	case 1:
		return "partly-cloudy", "Mostly clear"
	case 2:
		return "partly-cloudy", "Partly cloudy"
	case 3:
		return "cloudy", "Overcast"
	case 45, 48:
		return "fog", "Fog"
	case 51, 53, 55:
		return "rain", "Drizzle"
	case 56, 57, 66, 67:
		return "rain", "Freezing rain"
	case 61, 63, 65:
		return "rain", "Rain"
	case 80, 81, 82:
		return "rain", "Rain showers"
	case 71, 73, 75, 77, 85, 86:
		return "snow", "Snow"
	case 95, 96, 97, 99:
		return "storm", "Thunderstorms"
	default:
		return "unknown", "Conditions unavailable"
	}
}
func validSample(s Sample) bool {
	return !s.Time.IsZero() && finite(s.Temperature, -150, 100) && finite(s.FeelsLike, -200, 150) && finite(s.CloudCover, 0, 100) && finite(s.Wind, 0, 1000) && s.Code >= 0 && s.Code <= 999 && (s.RainChance == nil || finite(*s.RainChance, 0, 100))
}

// Validate also gates persisted cache data; corrupt or expired data never looks current.
func (f Forecast) Validate(now time.Time) error {
	if err := f.Location.Validate(); err != nil {
		return err
	}
	if f.RetrievedAt.IsZero() || f.RetrievedAt.After(now.Add(time.Minute)) || !validSample(f.Current) || f.Current.Time.After(f.RetrievedAt.Add(5*time.Minute)) || f.Current.Time.Before(f.RetrievedAt.Add(-45*time.Minute)) {
		return errors.New("Invalid weather freshness.")
	}
	if len(f.Hourly) < 1 || len(f.Hourly) > 400 || len(f.Daily) < 1 || len(f.Daily) > 16 {
		return errors.New("Invalid forecast coverage.")
	}
	for i, s := range f.Hourly {
		if !validSample(s) || (i > 0 && !s.Time.After(f.Hourly[i-1].Time)) {
			return errors.New("Invalid hourly forecast.")
		}
	}
	var previous string
	for _, d := range f.Daily {
		if _, err := time.Parse("2006-01-02", d.Date); err != nil || d.Date <= previous || !finite(d.Low, -150, 100) || !finite(d.High, -150, 100) || d.Low > d.High || d.Code < 0 || d.Code > 999 || d.Sunrise.IsZero() || !d.Sunset.After(d.Sunrise) || (d.RainChance != nil && !finite(*d.RainChance, 0, 100)) {
			return errors.New("Invalid daily forecast.")
		}
		previous = d.Date
	}
	if f.Current.Time.Before(f.Hourly[0].Time.Add(-time.Hour)) || !f.Current.Time.Before(f.Hourly[len(f.Hourly)-1].Time.Add(time.Hour)) {
		return errors.New("Current weather outside forecast coverage.")
	}
	return nil
}
func Temperature(c float64, units string) string {
	if units == "F" {
		c = c*9/5 + 32
	}
	if math.Abs(c) < .5 {
		c = 0
	}
	return fmt.Sprintf("%.0f°", c)
}

// IsDay uses A Weather App's geometric solar-position calculation. It stays
// correct when a cached observation spans sunset or sunrise; no frame clock is used.
func IsDay(t time.Time, lat, lon float64) bool {
	u := t.UTC()
	hour := float64(u.Hour()) + float64(u.Minute())/60 + float64(u.Second())/3600
	days := 365.
	if u.Year()%4 == 0 && (u.Year()%100 != 0 || u.Year()%400 == 0) {
		days = 366
	}
	g := 2 * math.Pi / days * (float64(u.YearDay()-1) + (hour-12)/24)
	eq := 229.18 * (.000075 + .001868*math.Cos(g) - .032077*math.Sin(g) - .014615*math.Cos(2*g) - .040849*math.Sin(2*g))
	dec := .006918 - .399912*math.Cos(g) + .070257*math.Sin(g) - .006758*math.Cos(2*g) + .000907*math.Sin(2*g) - .002697*math.Cos(3*g) + .00148*math.Sin(3*g)
	minutes := math.Mod(hour*60+eq+4*lon, 1440)
	if minutes < 0 {
		minutes += 1440
	}
	ha := (minutes/4 - 180) * math.Pi / 180
	lat *= math.Pi / 180
	return math.Sin(lat)*math.Sin(dec)+math.Cos(lat)*math.Cos(dec)*math.Cos(ha) >= 0
}

type View struct {
	Enabled, Available, Stale, Loading                                                                                     bool
	Location, ZIP, Units, Scene, Theme, Condition, Icon, Temperature, FeelsLike, Low, High, Rain, Updated, Status, Preview string
}

// Appearance follows the home sun even with weather disabled or unavailable.
// Background previews do not change the appearance schedule.
func Appearance(cfg Settings, now time.Time) string {
	if cfg.Theme == "light" || cfg.Theme == "dark" {
		return cfg.Theme
	}
	if IsDay(now, cfg.Location.Latitude, cfg.Location.Longitude) {
		return "light"
	}
	return "dark"
}

func MakeView(cfg Settings, f *Forecast, now time.Time, loading bool, failed bool) View {
	v := View{Enabled: cfg.Enabled, Location: cfg.Location.Name, ZIP: cfg.Location.ZIP, Units: cfg.Units, Scene: "neutral", Theme: Appearance(cfg, now), Status: "Weather unavailable", Loading: loading}
	if !cfg.Enabled {
		v.Status = "Weather is off"
		return v
	}
	if f != nil && f.Location.Key() == cfg.Location.Key() && f.Validate(now) == nil && now.Sub(f.RetrievedAt) >= 0 && now.Sub(f.RetrievedAt) <= Expire && now.Sub(f.Current.Time) <= Expire {
		v.Available = true
		v.Stale = failed || now.Sub(f.RetrievedAt) > Refresh || now.Sub(f.Current.Time) > 45*time.Minute
		kind, label := Condition(f.Current.Code)
		v.Condition = label
		v.Icon = kind
		v.Temperature = Temperature(f.Current.Temperature, cfg.Units)
		v.FeelsLike = Temperature(f.Current.FeelsLike, cfg.Units)
		zone, _ := time.LoadLocation(cfg.Location.Zone)
		date := now.In(zone).Format("2006-01-02")
		for _, d := range f.Daily {
			if d.Date == date {
				v.Low = Temperature(d.Low, cfg.Units)
				v.High = Temperature(d.High, cfg.Units)
				if d.RainChance != nil {
					v.Rain = fmt.Sprintf("%.0f%%", *d.RainChance)
				}
				break
			}
		}
		v.Updated = f.RetrievedAt.In(zone).Format("3:04 PM")
		v.Status = "Updated " + v.Updated
		if v.Stale {
			v.Status = "Last update " + v.Updated + " · stale"
		}
		if kind != "unknown" {
			v.Scene = kind
			if kind == "clear" || kind == "partly-cloudy" {
				v.Scene = "sunny"
			}
			if !IsDay(now, cfg.Location.Latitude, cfg.Location.Longitude) {
				v.Scene = "night"
				if kind == "clear" {
					v.Icon = "night"
				}
			}
		}
	} else if loading {
		v.Status = "Loading weather…"
	}
	if cfg.Background == "off" {
		v.Scene = "neutral"
	} else if cfg.Background != "live" {
		v.Scene = cfg.Background
		v.Preview = cfg.Background
	}
	return v
}
