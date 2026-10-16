package dashboard

import (
	"net/http"
	"net/url"
	"strconv"
)

func (a *App) todayWeather(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/weather", http.StatusSeeOther)
		return
	}
	p := a.base(w, r)
	p.View = "weather"
	p.WeatherModal = true
	if err := a.load(&p, r.URL.Query()); err != nil {
		a.fail(w, err)
		return
	}
	a.render(w, "weather-modal", p)
}
func (a *App) todayEvent(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, calURL("day", p.Today), http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	events, err := a.store.List("event", p.Today)
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, e := range eventsOn(events, p.Today, a.loc) {
		if e.ID == id {
			p.Entry = e
			p.Subtitle = p.Now.Format("Monday, January 2")
			p.Return = calURL("day", p.Today)
			a.render(w, "today-event-modal", p)
			return
		}
	}
	http.NotFound(w, r)
}
func (a *App) todaySummary(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if kind != "chores" && kind != "shopping" {
		http.NotFound(w, r)
		return
	}
	p := a.base(w, r)
	p.View = "today"
	if err := a.load(&p, url.Values{}); err != nil {
		a.fail(w, err)
		return
	}
	if kind == "chores" {
		filter := chorePersonFilter(r.URL.Query().Get("person"), p.ChorePeople)
		p.SummaryTitle, p.SummaryURL = "Today's chores", choreURL(filter, "", false)
		for _, e := range p.Chores {
			if matchesChorePerson(e, filter) {
				p.SummaryEntries = append(p.SummaryEntries, e)
			}
		}
	} else {
		filter, err := shopFilter(r.URL.Query().Get("store"), p.Shops)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p.SummaryTitle, p.SummaryURL = "Shopping list", "/shopping"
		if filter != "" {
			p.SummaryURL += "?store=" + url.QueryEscape(filter)
		}
		for _, e := range p.Shopping {
			if matchesShop(e, filter) {
				p.SummaryEntries = append(p.SummaryEntries, e)
			}
		}
	}
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, p.SummaryURL, http.StatusSeeOther)
		return
	}
	a.render(w, "today-summary-modal", p)
}
