package weather

import (
	"fmt"
	"time"
)

// ForecastView presents the same validated snapshot as Today. The underlying
// Forecast remains shared data for future calendar integrations.
type ForecastView struct {
	Current                                         View
	Hourly                                          []HourlyView
	Days                                            []DayView
	SelectedDate, HourlyTitle, HourlyCoverage, Zone string
	Wind, Sunrise, Sunset, SunLabel, Coverage       string
}

type HourlyView struct {
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
	if cfg.Units == "F" {
		v.Wind = fmt.Sprintf("%.0f mph", f.Current.Wind*2.2369362921)
	} else {
		v.Wind = fmt.Sprintf("%.0f km/h", f.Current.Wind*3.6)
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
		v.Hourly = append(v.Hourly, HourlyView{Time: h.Time.Format(time.RFC3339), Label: label, Date: at.Format("Mon, Jan 2"), Temperature: Temperature(h.Temperature, cfg.Units), Icon: icon, Condition: condition, Rain: chance(h.RainChance), PrecipitationLabel: hourlyPrecipitation(h.Code)})
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
	return v
}
