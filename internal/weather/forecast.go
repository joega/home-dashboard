package weather

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ForecastView presents the same validated snapshot as Today. The underlying
// Forecast remains shared data for future calendar integrations.
type ForecastView struct {
	Current                                         View
	HomeHourly                                      []HourlyView
	Summary, Gusts, Humidity, UV, TemperaturePoints string
	Hourly                                          []HourlyView
	Days                                            []DayView
	SelectedDate, HourlyTitle, HourlyCoverage, Zone string
	Wind, Sunrise, Sunset, SunLabel, Coverage       string
}

type HourlyView struct {
	TemperatureC                                                              float64
	Chance                                                                    int
	Time, Label, Date, Temperature, Icon, Condition, Rain, PrecipitationLabel string
}

type DayView struct {
	Date, Label, Caption, Icon, Condition, High, Low, Rain, PrecipitationLabel, URL string
	Selected                                                                        bool
}

func forecastIcon(code int, daylight bool) (string, string) {
	kind, label := Condition(code)
	if !daylight && (kind == "clear" || kind == "partly-cloudy") {
		return "night", label
	}
	return kind, label
}

func chance(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.0f%%", *v)
}

func hourlyPrecipitation(code int) string {
	kind, _ := Condition(code)
	if kind == "snow" {
		return "Snow chance"
	}
	return "Rain chance"
}

func MakeForecastView(cfg Settings, f *Forecast, now time.Time, requestedDate string, loading, failed bool) ForecastView {
	v := ForecastView{Current: MakeView(cfg, f, now, loading, failed), HourlyTitle: "Next 24 hours", Zone: cfg.Location.Zone}
	if !v.Current.Available {
		return v
	}
	zone, _ := time.LoadLocation(cfg.Location.Zone)
	local := now.In(zone)
	today := local.Format("2006-01-02")
	last := local.AddDate(0, 0, 6).Format("2006-01-02")
	tomorrow := local.AddDate(0, 0, 1).Format("2006-01-02")
	for _, d := range f.Daily {
		if d.Date < today || d.Date > last || len(v.Days) == 7 {
			continue
		}
		date, _ := time.ParseInLocation("2006-01-02", d.Date, zone)
		label := date.Format("Mon")
		if d.Date == today {
			label = "Today"
		} else if d.Date == tomorrow {
			label = "Tomorrow"
		}
		icon, condition := forecastIcon(d.Code, true)
		v.Days = append(v.Days, DayView{Date: d.Date, Label: label, Caption: date.Format("Jan 2"), Icon: icon, Condition: condition, High: Temperature(d.High, cfg.Units), Low: Temperature(d.Low, cfg.Units), Rain: chance(d.RainChance), PrecipitationLabel: precipitationLabel(f, d, zone), URL: "/weather?date=" + d.Date})
		if requestedDate == d.Date {
			v.SelectedDate = d.Date
			v.HourlyTitle = label + " · " + date.Format("Jan 2")
		}
	}
	if len(v.Days) < 7 {
		v.Coverage = fmt.Sprintf("%d of 7 days available", len(v.Days))
	}
	selected := v.SelectedDate
	if selected == "" {
		selected = today
	}
	for i := range v.Days {
		v.Days[i].Selected = v.Days[i].Date == selected
	}
	for _, d := range f.Daily {
		if d.Date == selected {
			v.Sunrise, v.Sunset = d.Sunrise.In(zone).Format("3:04 PM"), d.Sunset.In(zone).Format("3:04 PM")
			v.SunLabel = "Today"
			if selected != today {
				date, _ := time.ParseInLocation("2006-01-02", selected, zone)
				v.SunLabel = date.Format("Mon")
			}
			break
		}
	}
	v.Summary = summary(f, now, zone)
	v.Gusts, v.Humidity, v.UV = "—", "—", "—"
	if f.Current.WindGust != nil {
		v.Gusts = windSpeed(*f.Current.WindGust, cfg.Units)
	}
	if f.Current.Humidity != nil {
		v.Humidity = fmt.Sprintf("%.0f%%", *f.Current.Humidity)
	}
	for _, d := range f.Daily {
		if d.Date == selected && d.UV != nil {
			v.UV = fmt.Sprintf("%.1f", *d.UV)
		}
	}
	if cfg.Units == "F" {
		v.Wind = fmt.Sprintf("%.0f mph", f.Current.Wind*2.2369362921)
	} else {
		v.Wind = fmt.Sprintf("%.0f km/h", f.Current.Wind*3.6)
	}
	if f.Current.WindDirection != nil {
		v.Wind = []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}[int(math.Round(*f.Current.WindDirection/45))%8] + " " + v.Wind
	}
	for _, h := range f.Hourly {
		if !h.Time.Add(time.Hour).After(now) {
			continue
		}
		at := h.Time.In(zone)
		if v.SelectedDate != "" {
			if at.Format("2006-01-02") != v.SelectedDate {
				continue
			}
		} else if !h.Time.Before(now.Truncate(time.Hour).Add(24 * time.Hour)) {
			break
		}
		icon, condition := forecastIcon(h.Code, h.IsDay)
		label := at.Format("3 PM")
		if !h.Time.After(now) {
			label = "Now"
		}
		temperature := h.Temperature
		if label == "Now" {
			temperature, icon, condition = f.Current.Temperature, v.Current.Icon, v.Current.Condition
		}
		v.Hourly = append(v.Hourly, HourlyView{TemperatureC: temperature, Chance: rainBar(h.RainChance), Time: h.Time.Format(time.RFC3339), Label: label, Date: at.Format("Mon, Jan 2"), Temperature: Temperature(temperature, cfg.Units), Icon: icon, Condition: condition, Rain: chance(h.RainChance), PrecipitationLabel: hourlyPrecipitation(h.Code)})
	}
	if v.SelectedDate == "" && len(v.Hourly) < 24 {
		v.HourlyCoverage = fmt.Sprintf("%d of 24 hours available", len(v.Hourly))
	}
	// Distinguish repeated fall-back hours without dropping either instant.
	counts := map[string]int{}
	for _, h := range v.Hourly {
		counts[h.Date+" "+h.Label]++
	}
	for i, h := range v.Hourly {
		if h.Label != "Now" && counts[h.Date+" "+h.Label] > 1 {
			at, _ := time.Parse(time.RFC3339, h.Time)
			v.Hourly[i].Label = at.In(zone).Format("3 PM MST")
		}
	}

	if v.SelectedDate == "" {
		v.HomeHourly = append(v.HomeHourly, v.Hourly[:min(6, len(v.Hourly))]...)
	}
	if len(v.Hourly) > 1 {
		low, high := v.Hourly[0].TemperatureC, v.Hourly[0].TemperatureC
		for _, h := range v.Hourly {
			low = math.Min(low, h.TemperatureC)
			high = math.Max(high, h.TemperatureC)
		}
		points := make([]string, 0, len(v.Hourly))
		for i, h := range v.Hourly {
			points = append(points, fmt.Sprintf("%.1f,%.1f", 20+float64(i)*860/float64(len(v.Hourly)-1), 52-(h.TemperatureC-low)/math.Max(high-low, 1)*40))
		}
		v.TemperaturePoints = strings.Join(points, " ")
	}
	return v
}

func windSpeed(ms float64, units string) string {
	if units == "F" {
		return fmt.Sprintf("%.0f mph", ms*2.2369362921)
	}
	return fmt.Sprintf("%.0f km/h", ms*3.6)
}
func rainBar(chance *float64) int {
	if chance == nil {
		return 0
	}
	return int(math.Round(*chance))
}

// Headlines use hourly probabilities, never imply minute-level rain timing,
// and promise a dry window only when all twelve consecutive hours are known.
func summary(f *Forecast, now time.Time, zone *time.Location) string {
	start := now.Truncate(time.Hour)
	window := []Sample{}
	for _, h := range f.Hourly {
		if !h.Time.Before(start) && h.Time.Before(start.Add(12*time.Hour)) {
			window = append(window, h)
		}
	}
	for _, h := range window {
		if h.RainChance != nil && *h.RainChance >= 50 {
			kind, label := Condition(h.Code)
			noun := "Rain"
			if kind == "snow" {
				noun = "Snow"
			}
			if kind == "storm" {
				noun = "Thunderstorms"
			}
			if label == "Freezing rain" {
				noun = label
			}
			if !h.Time.After(now.Add(time.Hour)) {
				return noun + " likely in the next hour"
			}
			return noun + " likely around " + h.Time.In(zone).Format("3 PM")
		}
	}
	if f.Current.WindGust != nil && *f.Current.WindGust >= 12.5 {
		return "Strong gusts — a windy spell"
	}
	dry := len(window) == 12
	for i, h := range window {
		dry = dry && h.Time.Equal(start.Add(time.Duration(i)*time.Hour)) && h.RainChance != nil && *h.RainChance < 20
	}
	if dry {
		return "Mostly dry for the next 12 hours"
	}
	return "Your next few hours, at a glance"
}
