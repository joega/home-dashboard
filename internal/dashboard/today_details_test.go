package dashboard

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTodayModalsRemainReadOnlyAndReuseCachedWeather(t *testing.T) {
	s, _ := testStore(t)
	zone, _ := time.LoadLocation("America/New_York")
	day := time.Now().In(zone).Format("2006-01-02")
	for _, e := range []Entry{{Kind: "event", Title: "Today's meeting", Date: day, Start: "15:00", Location: "School"}, {Kind: "event", Title: "Future only", Date: "2099-01-01", Start: "15:00"}, {Kind: "chore", Title: "Due now", Date: day}, {Kind: "chore", Title: "Not due", Date: "2099-01-01"}, {Kind: "shopping", Title: "Milk", Quantity: "1 carton"}} {
		if err := s.Save(e); err != nil {
			t.Fatal(err)
		}
	}
	provider := &forecastPageProvider{}
	a, err := New(s, zone, provider)
	if err != nil {
		t.Fatal(err)
	}
	a.weather.Refresh(context.Background(), time.Now())
	get := func(path string, hx bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://household.test"+path, nil)
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	home := get("/", false).Body.String()
	for _, want := range []string{`hx-get="/today/weather"`, `class="today-grid"`, `class="agenda"`, `hx-target="#drawer"`} {
		if !strings.Contains(home, want) {
			t.Fatal("missing Today action", want)
		}
	}

	if strings.Contains(home, "today-next") || strings.Contains(home, "data-today-clock") {
		t.Fatal("discarded Today redesign returned")
	}
	for _, want := range []string{
		`href="/chores" hx-boost="true" hx-target="#content"`,
		`href="/shopping" hx-boost="true" hx-target="#content"`,
		`class="today-weather-page-link" href="/weather" hx-boost="true" hx-target="#content"`,
		`aria-label="Preview Today&#39;s meeting"`,
	} {
		if !strings.Contains(home, want) {
			t.Fatal("preview/full-page navigation contract missing", want)
		}
	}
	for _, tc := range []struct{ path, want, absent string }{{"/today/weather", "Next 24 hours", "<!doctype"}, {"/today/summary/chores?person=none", "Due now", "Not due"}, {"/today/summary/shopping?store=none", "1 carton", "Not due"}, {"/today/event/1", "School", "hx-post="}} {
		w := get(tc.path, true)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `role="dialog"`) || !strings.Contains(body, tc.want) || strings.Contains(body, tc.absent) {
			t.Fatal(tc.path, w.Code, body)
		}
	}
	body := get("/today/weather?date="+time.Now().In(zone).AddDate(0, 0, 1).Format("2006-01-02"), true).Body.String()
	if !strings.Contains(body, `hx-get="/today/weather?date=`) || strings.Contains(body, `hx-target="#content" aria-current="date"`) {
		t.Fatal("weather date navigates out of popup")
	}
	for _, path := range []string{"/today/event/2", "/today/event/nope", "/today/summary/other"} {
		if get(path, true).Code != 404 {
			t.Fatal("invalid detail accepted", path)
		}
	}
	if get("/today/summary/shopping?store=nope", true).Code != 400 {
		t.Fatal("invalid store accepted")
	}
	for _, path := range []string{"/today/weather", "/today/event/1", "/today/summary/chores"} {
		if get(path, false).Code != 303 {
			t.Fatal("no-JS fallback missing", path)
		}
	}
	if provider.fetches != 1 || provider.resolves != 0 {
		t.Fatal("opening details fetched weather", provider.fetches, provider.resolves)
	}
}
