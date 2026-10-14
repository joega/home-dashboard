package dashboard

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Hour struct {
	Label string
	Top   int
}
type Block struct {
	Entry                    Entry
	Top, Height, Left, Width int
	More                     int
}
type Lane struct {
	Date, Label, Caption string
	Color                int
	Today                bool
	AllDay, Events       []Entry
	Blocks               []Block
	More                 int
}

func validMode(v string) bool         { return v == "day" || v == "3day" || v == "week" || v == "month" }
func calURL(mode, date string) string { return "/calendar?mode=" + mode + "&date=" + date }
func owner(e Entry) string {
	v := strings.TrimSpace(e.Owner)
	if v == "" || v == "Anyone" {
		return "Household"
	}
	return v
}
func color(e Entry) int {
	key := owner(e)
	if e.PersonID > 0 {
		key = fmt.Sprintf("person:%d", e.PersonID)
	}
	hash := sha256.Sum256([]byte(key))
	return int(hash[0]%4) + 1
}
func minutes(v string) int {
	t, e := time.Parse("15:04", v)
	if e != nil {
		return 0
	}
	return t.Hour()*60 + t.Minute()
}
func (a *App) loadCalendar(p *Page, q url.Values) error {
	p.Title = "Calendar"
	mode := q.Get("mode")
	if !validMode(mode) {
		mode = p.CalendarMode
	}
	if !validMode(mode) {
		mode = "day"
	}
	raw := q.Get("date")
	if raw == "" && q.Get("month") != "" {
		raw = q.Get("month") + "-01"
		if q.Get("mode") == "" {
			mode = "month"
		}
	}
	date, err := time.ParseInLocation("2006-01-02", raw, a.loc)
	if err != nil {
		date = time.Date(p.Now.Year(), p.Now.Month(), p.Now.Day(), 0, 0, 0, 0, a.loc)
	}
	p.CalendarMode = mode
	p.CalDate = date.Format("2006-01-02")
	p.MonthLabel = date.Format("January 2006")
	p.Subtitle = p.MonthLabel
	p.DayURL = calURL("day", p.CalDate)
	p.ThreeURL = calURL("3day", p.CalDate)
	p.WeekURL = calURL("week", p.CalDate)
	p.MonthURL = calURL("month", p.CalDate)
	p.TodayURL = calURL(mode, p.Today)
	step := 1
	if mode == "3day" {
		step = 3
	}
	if mode == "week" {
		step = 7
	}
	prev, next := date.AddDate(0, 0, -step), date.AddDate(0, 0, step)
	if mode == "month" {
		first := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, a.loc)
		prev = first.AddDate(0, -1, 0)
		next = first.AddDate(0, 1, 0)
	}
	p.PrevURL = calURL(mode, prev.Format("2006-01-02"))
	p.NextURL = calURL(mode, next.Format("2006-01-02"))
	p.CalTitle = date.Format("Monday 2")
	if mode == "3day" {
		p.CalTitle = date.Format("Mon 2") + " – " + date.AddDate(0, 0, 2).Format("Mon 2")
	}
	if mode == "week" {
		p.CalTitle = date.Format("Jan 2") + " – " + date.AddDate(0, 0, 6).Format("Jan 2")
	}
	if mode == "month" {
		p.CalTitle = date.Format("January")
		p.Subtitle = date.Format("2006")
		start := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, a.loc)
		p.Month = start.Format("2006-01")
		lead := (int(start.Weekday()) + 6) % 7
		p.Weeks = 6
		for i := 0; i < 42; i++ {
			d := start.AddDate(0, 0, i-lead)
			c := Cell{Date: d.Format("2006-01-02"), Day: d.Day(), Other: d.Month() != start.Month(), Today: d.Format("2006-01-02") == p.Today}
			c.Events = eventsOn(p.Events, c.Date, a.loc)
			p.Cells = append(p.Cells, c)
		}
		return nil
	}
	count := 1
	if mode == "3day" {
		count = 3
	}
	if mode == "week" {
		count = 7
	}
	// Day view shares one timeline across the household. Multi-day views keep
	// one lane per date; ownership is shown on each event instead of in lanes.
	for i := 0; i < count; i++ {
		d := date.AddDate(0, 0, i)
		lane := Lane{Date: d.Format("2006-01-02"), Label: d.Format("Mon 2"), Caption: d.Format("January"), Today: d.Format("2006-01-02") == p.Today}
		if mode == "day" {
			lane.Label, lane.Caption = "Everyone", ""
		}
		for _, e := range eventsOn(p.Events, lane.Date, a.loc) {
			if e.Start == "" {
				lane.AllDay = append(lane.AllDay, e)
			} else {
				lane.Events = append(lane.Events, e)
			}
		}
		lane.Blocks = layout(lane.Events)
		if mode == "week" && len(lane.Events) > 5 {
			lane.More = len(lane.Events) - 5
			lane.Events = lane.Events[:5]
		}
		p.Lanes = append(p.Lanes, lane)
	}
	p.NowTop = -1
	p.NowIndex = -1
	if mode == "day" && p.CalDate == p.Today {
		p.NowIndex = 0
	} else {
		for i, lane := range p.Lanes {
			if lane.Today {
				p.NowIndex = i
				break
			}
		}
	}
	nowMinute := p.Now.Hour()*60 + p.Now.Minute()
	if p.NowIndex >= 0 && nowMinute >= 0 && nowMinute < 1440 {
		p.NowTop = nowMinute * 72 / 60
	}
	p.ScrollTo = 576
	if p.NowTop >= 0 {
		p.ScrollTo = p.NowTop - 170
		if p.ScrollTo < 0 {
			p.ScrollTo = 0
		}
	}
	for h := 0; h <= 24; h++ {
		label := fmt.Sprintf("%d AM", h)
		if h == 0 || h == 24 {
			label = "12 AM"
		} else if h == 12 {
			label = "Noon"
		} else if h > 12 {
			label = fmt.Sprintf("%d PM", h-12)
		}
		p.Hours = append(p.Hours, Hour{label, h * 72})
	}
	return nil
}
func layout(entries []Entry) []Block {
	list := append([]Entry(nil), entries...)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Start < list[j].Start })
	var result []Block
	for len(list) > 0 {
		n := 1
		finish := endMinute(list[0])
		for n < len(list) && minutes(list[n].Start) < finish {
			if endMinute(list[n]) > finish {
				finish = endMinute(list[n])
			}
			n++
		}
		cluster := list[:n]
		list = list[n:]
		ends := []int{}
		cols := make([]int, len(cluster))
		for i, e := range cluster {
			start := minutes(e.Start)
			c := 0
			for c < len(ends) && ends[c] > start {
				c++
			}
			if c == len(ends) {
				ends = append(ends, 0)
			}
			ends[c] = endMinute(e)
			cols[i] = c
		}
		total := len(ends)
		shown := total
		if shown > 2 {
			shown = 2
		}
		extra, extraStart, extraEnd := 0, 1440, 0
		for i, e := range cluster {
			start, end := minutes(e.Start), endMinute(e)
			if end <= 0 || start >= 1440 {
				continue
			}
			if start < 0 {
				start = 0
			}
			if end > 1440 {
				end = 1440
			}
			if total > 2 && cols[i] > 0 {
				extra++
				if start < extraStart {
					extraStart = start
				}
				if end > extraEnd {
					extraEnd = end
				}
				continue
			}
			top := start * 72 / 60
			height := (end-start)*72/60 - 3
			if height < 44 {
				height = 44
			}
			result = append(result, Block{Entry: e, Top: top, Height: height, Left: cols[i] * 100 / shown, Width: 100 / shown})
		}
		if extra > 0 {
			height := (extraEnd-extraStart)*72/60 - 3
			if height < 44 {
				height = 44
			}
			result = append(result, Block{Top: extraStart * 72 / 60, Height: height, Left: 50, Width: 50, More: extra})
		}
	}
	return result
}
func endMinute(e Entry) int {
	if e.End != "" {
		return minutes(e.End)
	}
	return minutes(e.Start) + 60
}
func (a *App) day(w http.ResponseWriter, r *http.Request) {
	d, err := time.ParseInLocation("2006-01-02", r.PathValue("date"), a.loc)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p := a.base(w, r)
	p.Return = returnPath(r)
	p.Entry.Date = d.Format("2006-01-02")
	p.Subtitle = d.Format("Monday, January 2")
	entries, err := a.store.List("event", p.Today)
	if err != nil {
		a.fail(w, err)
		return
	}
	p.Events = eventsOn(entries, p.Entry.Date, a.loc)
	a.render(w, "day", p)
}
