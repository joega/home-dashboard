package dashboard

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"home-dashboard/internal/sports"
	"home-dashboard/internal/weather"
	"home-dashboard/web"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Cell struct {
	Date         string
	Day          int
	Other, Today bool
	Events       []Entry
}
type Page struct {
	Sports                                                                                                             sports.View
	SportsSettings                                                                                                     sports.Settings
	SportsTeams                                                                                                        []sports.Team
	Recipe                                                                                                             Recipe
	Recipes                                                                                                            []Recipe
	RecipeFilter                                                                                                       RecipeFilter
	RecipeTags, Meals, RecipeCategories, DraftKeys                                                                     []string
	CookbookMode, RecipeReturn, RecipeBase, RecipeImage, RecipePrev, RecipeNext, DraftKey, BatchToken, ExploreCategory string
	RecipeCreateKey                                                                                                    string
	RecipeCount                                                                                                        int
	RecipeShopping                                                                                                     []RecipeShoppingItem
	ShoppingRetry                                                                                                      bool
	ExplorePreview                                                                                                     bool
	ExistingRecipe                                                                                                     int64

	ChoreSummary, ShoppingSummary                                                                    []SummaryGroup
	ChorePending, ShoppingPending                                                                    int
	PersonFilter                                                                                     string
	DisplaySettings                                                                                  DisplaySettings
	Weather                                                                                          weather.View
	WeatherSettings                                                                                  weather.Settings
	Notice                                                                                           string
	Shops                                                                                            []Shop
	Shop                                                                                             Shop
	ShopFilter, SettingsSection                                                                      string
	HasArchivedShops                                                                                 bool
	AllSchedules                                                                                     bool
	People                                                                                           []Person
	Person                                                                                           Person
	HasArchived                                                                                      bool
	CalendarMode, CalDate, CalTitle, PrevURL, NextURL, TodayURL, DayURL, ThreeURL, WeekURL, MonthURL string
	Lanes                                                                                            []Lane
	Hours                                                                                            []Hour
	NowTop, NowIndex, ScrollTo                                                                       int

	View, Title, Subtitle, Today, Month, MonthLabel, PrevMonth, NextMonth, Routine, CSRF, AssetVersion string
	Now                                                                                                time.Time
	Zone                                                                                               string
	Demo                                                                                               bool
	Events, Chores, Shopping, Completed                                                                []Entry
	Cells                                                                                              []Cell
	Weeks                                                                                              int
	Entry                                                                                              Entry
	FormTitle, Error, Return                                                                           string
}
type Row struct {
	Entry        Entry
	CSRF, Return string
}
type App struct {
	recipeClient *http.Client
	weather      *weather.Service
	sports       *sports.Service
	store        *Store
	loc          *time.Location
	templates    *template.Template
	handler      http.Handler
	version      string
}

func New(store *Store, loc *time.Location, providers ...weather.Provider) (*App, error) {
	functions := template.FuncMap{
		"clock": func(t time.Time) string { return t.Format("3:04 PM") },
		"timeLabel": func(v string) string {
			t, e := time.Parse("15:04", v)
			if e != nil {
				return "All day"
			}
			return t.Format("3:04 PM")
		},
		"first": func(n int, v []Entry) []Entry {
			if len(v) > n {
				return v[:n]
			}
			return v
		},
		"initials":       initials,
		"repeatLabel":    repeatLabel,
		"weekdayChecked": func(mask, day int) bool { return mask&(1<<day) != 0 },
		"shopRow":        func(shop Shop, p Page) ShopRow { return ShopRow{shop, p.CSRF} },
		"joinLines":      func(v []string) string { return strings.Join(v, "\n") },
		"joinTags":       func(v []string) string { return strings.Join(v, ", ") },
		"recipeCard":     makeRecipeCard,
		"recipePickURL":  recipePickURL,
		"recipePath":     recipePath,
		"mealRef":        func(v string) string { return strings.TrimPrefix(v, "mdb:") },
		"recipeURLQuery": url.QueryEscape,
		"choreURL":       choreURL,
		"personSelected": func(id int64, filter string) bool { return strconv.FormatInt(id, 10) == filter },
		"shopSelected":   func(id int64, filter string) bool { return strconv.FormatInt(id, 10) == filter },
		"personRow":      func(person Person, p Page) PersonRow { return PersonRow{Person: person, CSRF: p.CSRF} },
		"inc":            func(n int) int { return n + 1 },
		"color":          color,
		"row":            func(e Entry, p Page) Row { return Row{e, p.CSRF, p.Return} },
	}
	templates, err := template.New("").Funcs(functions).ParseFS(web.Files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	assets, _ := fs.Sub(web.Files, "static")
	version, err := applicationVersion(web.Files, buildRevision)
	if err != nil {
		return nil, err
	}
	a := &App{store: store, loc: loc, templates: templates, version: version}
	var provider weather.Provider = weather.NewClient()
	if len(providers) > 0 {
		provider = providers[0]
	}
	a.weather = weather.NewService(store, provider)
	a.sports = sports.NewService(store, sports.NewClient(), loc)
	a.recipeClient = recipeHTTPClient()
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := store.db.PingContext(r.Context()); err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /version", a.applicationVersion)
	mux.HandleFunc("GET /clock", a.clock)
	mux.HandleFunc("GET /appearance", a.appearance)
	mux.HandleFunc("GET /frame/display-policy", a.displayPolicy)
	mux.HandleFunc("POST /settings/display", a.saveDisplay)
	for _, route := range []string{"/", "/calendar", "/chores", "/shopping", "/settings", "/sports"} {
		mux.HandleFunc("GET "+route, a.page)
	}
	mux.HandleFunc("GET /forms/{kind}/{id}", a.form)
	mux.HandleFunc("GET /day/{date}", a.day)
	mux.HandleFunc("POST /entries/{kind}/{id}", a.save)
	mux.HandleFunc("POST /entries/{id}/complete", a.complete)
	mux.HandleFunc("POST /entries/{id}/delete", a.delete)
	mux.HandleFunc("POST /shopping/clear", a.clearShopping)
	mux.HandleFunc("POST /reset-demo", a.resetDemo)
	mux.HandleFunc("POST /settings/weather", a.saveWeather)
	mux.HandleFunc("POST /settings/sports", a.saveSportsSettings)
	mux.HandleFunc("GET /sports/logos/{team}", a.sportsLogo)
	mux.HandleFunc("GET /people/{id}/edit", a.personForm)
	mux.HandleFunc("GET /people/{id}/photo", a.personPhoto)
	mux.HandleFunc("POST /people/{id}", a.savePerson)
	mux.HandleFunc("POST /people/{id}/status", a.personStatus)
	mux.HandleFunc("GET /stores/{id}/edit", a.shopForm)
	mux.HandleFunc("POST /stores/{id}", a.saveShop)
	mux.HandleFunc("POST /stores/{id}/status", a.shopStatus)
	a.registerCookbook(mux)
	a.handler = a.protect(mux)
	return a, nil
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }
func (a *App) protect(next http.Handler) http.Handler {
	cross := http.NewCrossOriginProtection()
	return cross.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" {
			var parseErr error
			if (strings.HasPrefix(r.URL.Path, "/people/") || strings.HasPrefix(r.URL.Path, "/cookbook")) && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				r.Body = http.MaxBytesReader(w, r.Body, (4<<20)+(128<<10))
				parseErr = r.ParseMultipartForm(4 << 20)
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
			} else {
				limit := int64(16 << 10)
				if strings.HasPrefix(r.URL.Path, "/cookbook") {
					limit = 128 << 10
				}
				r.Body = http.MaxBytesReader(w, r.Body, limit)
				parseErr = r.ParseForm()
			}
			if parseErr != nil {
				http.Error(w, "Invalid form", 400)
				return
			}
			c, err := r.Cookie("hq_csrf")
			if err != nil || len(c.Value) != 64 || subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.Form.Get("csrf"))) != 1 {
				http.Error(w, "Reload the page and try again.", 403)
				return
			}
		}
		next.ServeHTTP(w, r)
	}))
}
func (a *App) base(w http.ResponseWriter, r *http.Request) Page {
	p := Page{Now: time.Now().In(a.loc), Zone: a.loc.String(), AssetVersion: a.version, Demo: a.store.Demo()}
	p.Today = p.Now.Format("2006-01-02")
	if c, err := r.Cookie("hq_calendar_mode"); err == nil && validMode(c.Value) {
		p.CalendarMode = c.Value
	}
	if c, err := r.Cookie("hq_csrf"); err == nil && len(c.Value) == 64 {
		p.CSRF = c.Value
	} else {
		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			panic(err)
		}
		p.CSRF = hex.EncodeToString(token[:])
		http.SetCookie(w, &http.Cookie{Name: "hq_csrf", Value: p.CSRF, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	}
	return p
}
func (a *App) render(w http.ResponseWriter, name string, p Page) {
	var buf bytes.Buffer
	if err := a.templates.ExecuteTemplate(&buf, name, p); err != nil {
		log.Print(err)
		http.Error(w, "Unable to render dashboard", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}
func (a *App) page(w http.ResponseWriter, r *http.Request) {
	view := strings.Trim(r.URL.Path, "/")
	if view == "" {
		view = "today"
	}
	if view != "today" && view != "calendar" && view != "chores" && view != "shopping" && view != "settings" && view != "sports" {
		http.NotFound(w, r)
		return
	}
	p := a.base(w, r)
	p.View = view
	p.Return = r.URL.RequestURI()
	if err := a.load(&p, r.URL.Query()); err != nil {
		log.Print(err)
		http.Error(w, "Unable to read household data", 500)
		return
	}
	if p.View == "calendar" {
		http.SetCookie(w, &http.Cookie{Name: "hq_calendar_mode", Value: p.CalendarMode, Path: "/", MaxAge: 31536000, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	}
	name := "page"
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
		name = "fragment"
		if current, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil && current.RequestURI() != r.URL.RequestURI() {
			w.Header().Set("HX-Push-Url", r.URL.RequestURI())
		}
	}
	a.render(w, name, p)
}
func (a *App) load(p *Page, q url.Values) error {
	var err error
	p.WeatherSettings, err = a.store.WeatherSettings()
	if err != nil {
		return err
	}
	p.Weather = a.weather.View(p.WeatherSettings, p.Now)
	if p.View == "today" || p.View == "sports" || p.View == "settings" {
		if err = a.loadSports(p, q); err != nil {
			return err
		}
	}
	p.DisplaySettings, err = a.store.DisplaySettings()
	if err != nil {
		return err
	}
	p.People, err = a.store.People()
	if err != nil {
		return err
	}
	for _, person := range p.People {
		if !person.Active {
			p.HasArchived = true
		}
	}
	p.Shops, err = a.store.Shops()
	if err != nil {
		return err
	}
	for _, shop := range p.Shops {
		if !shop.Active {
			p.HasArchivedShops = true
		}
	}
	p.Events, err = a.store.List("event", p.Today)
	if err != nil {
		return err
	}
	p.Chores, err = a.store.List("chore", p.Today)
	if err != nil {
		return err
	}
	p.Shopping, err = a.store.List("shopping", p.Today)
	if err != nil {
		return err
	}
	switch p.View {
	case "settings":
		p.SettingsSection = q.Get("section")
		if p.SettingsSection != "stores" && p.SettingsSection != "weather" && p.SettingsSection != "display" && p.SettingsSection != "sports" {
			p.SettingsSection = "people"
		}
		p.Title = "Settings"
		p.Subtitle = "Your household."
	case "today":
		p.Title = "Today"
		p.Subtitle = p.Now.Format("Monday, January 2")
		p.Events = eventsOn(p.Events, p.Today, a.loc)
		var dueChores []Entry
		for _, e := range p.Chores {
			if e.Due && !e.Done {
				dueChores = append(dueChores, e)
			}
		}
		p.Chores = dueChores
		var active []Entry
		for _, e := range p.Shopping {
			if !e.Done {
				active = append(active, e)
			}
		}
		p.Shopping = active
		p.ChoreSummary, p.ChorePending = pendingGroups(p.Chores, true)
		p.ShoppingSummary, p.ShoppingPending = pendingGroups(p.Shopping, false)
	case "sports":
		p.Title = "Sports"
		p.Subtitle = ""
	case "calendar":
		return a.loadCalendar(p, q)
	case "chores":
		p.PersonFilter = chorePersonFilter(q.Get("person"), p.People)
		p.Title = "Chores"
		p.Subtitle = "A little teamwork, every day."
		p.AllSchedules = q.Get("schedules") == "all"
		p.Routine = q.Get("routine")
		if p.Routine != "Morning" && p.Routine != "Afternoon" && p.Routine != "Evening" {
			p.Routine = ""
		}
		var active []Entry
		for _, e := range p.Chores {
			if !matchesChorePerson(e, p.PersonFilter) {
				continue
			}
			if !p.AllSchedules && !e.Due {
				continue
			}
			if p.Routine != "" && e.Routine != p.Routine {
				continue
			}
			if e.Done {
				p.Completed = append(p.Completed, e)
			} else {
				active = append(active, e)
			}
		}
		p.Chores = active
	case "shopping":
		if n, e := strconv.Atoi(q.Get("recipe_added")); e == nil && n >= 0 && n <= 80 {
			word := "ingredients"
			if n == 1 {
				word = "ingredient"
			}
			p.Notice = fmt.Sprintf("Added %d %s to Shopping. Matching pending items were kept.", n, word)
		}
		p.ShopFilter, _ = shopFilter(q.Get("store"), p.Shops)
		p.Title = "Shopping"
		p.Subtitle = "The next trip, sorted."
		var active []Entry
		for _, e := range p.Shopping {
			if !matchesShop(e, p.ShopFilter) {
				continue
			}
			if e.Done {
				p.Completed = append(p.Completed, e)
			} else {
				active = append(active, e)
			}
		}
		p.Shopping = active
	}
	return nil
}
func (a *App) clock(w http.ResponseWriter, r *http.Request) { a.render(w, "clock", a.base(w, r)) }
func returnPath(r *http.Request) string {
	value := r.FormValue("return")
	if value == "" {
		value = r.URL.Query().Get("return")
	}
	u, err := url.Parse(value)
	if err == nil && u.IsAbs() == false && u.Host == "" && (u.Path == "/" || u.Path == "/calendar" || u.Path == "/chores" || u.Path == "/shopping" || u.Path == "/settings") {
		return u.RequestURI()
	}
	return "/"
}
func (a *App) form(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = returnPath(r)
	kind := r.PathValue("kind")
	if kind != "event" && kind != "chore" && kind != "shopping" {
		http.NotFound(w, r)
		return
	}
	p.Entry = Entry{Kind: kind, Owner: "Anyone", Routine: "Morning", Daily: kind == "chore", Date: p.Today}
	if kind == "event" {
		p.Entry.Owner = "Household"
	}
	if date := r.URL.Query().Get("date"); date != "" {
		if _, err := time.Parse("2006-01-02", date); err == nil {
			p.Entry.Date = date
		}
	}
	if r.PathValue("id") != "new" {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p.Entry, err = a.store.Get(id, p.Today)
		if err != nil || p.Entry.Kind != kind {
			http.NotFound(w, r)
			return
		}
	}
	var err error
	p.People, err = a.store.People()
	if err != nil {
		a.fail(w, err)
		return
	}
	p.Shops, err = a.store.Shops()
	if err != nil {
		a.fail(w, err)
		return
	}
	if kind == "shopping" && p.Entry.ID == 0 {
		u, _ := url.Parse(p.Return)
		id, _ := strconv.ParseInt(u.Query().Get("store"), 10, 64)
		for _, shop := range p.Shops {
			if shop.Active && shop.ID == id {
				p.Entry.ShopID = id
			}
		}
	}
	p.Entry = normalizeSchedule(p.Entry)
	if kind == "chore" && p.Entry.Date == "" && p.Entry.Repeat != "" {
		p.Entry.Date = p.Today
	}
	a.formTitle(&p)
	a.render(w, "form", p)
}
func (a *App) formTitle(p *Page) {
	names := map[string]string{"event": "event", "chore": "chore", "shopping": "item"}
	p.FormTitle = "New " + names[p.Entry.Kind]
	if p.Entry.ID != 0 {
		p.FormTitle = "Edit " + names[p.Entry.Kind]
	}
}
func (a *App) save(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = returnPath(r)
	kind := r.PathValue("kind")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 0 {
		http.NotFound(w, r)
		return
	}
	e := Entry{ID: id, Kind: kind, Title: strings.TrimSpace(r.FormValue("title")), Owner: strings.TrimSpace(r.FormValue("owner")), Routine: r.FormValue("routine"), Quantity: strings.TrimSpace(r.FormValue("quantity")), Date: r.FormValue("date"), Start: r.FormValue("start"), End: r.FormValue("end"), Daily: r.FormValue("daily") == "on", Location: strings.TrimSpace(r.FormValue("location")), RepeatUntil: r.FormValue("repeat_until")}
	if kind == "chore" || kind == "event" {
		e.Repeat, e.RepeatEvery, e.RepeatDays, err = parseSchedule(r.FormValue("repeat"), r.FormValue("repeat_every"), r.Form["repeat_days"])
		if err == nil {
			err = a.assignment(&e, r)
		}
		e = normalizeSchedule(e)
		if kind == "chore" && e.Repeat != "" && e.Date == "" && r.Form.Has("date") {
			err = errors.New("Choose when this schedule starts.")
		}
		if kind == "chore" && e.Repeat != "" && e.Date == "" && !r.Form.Has("date") {
			e.Date = p.Today
		}
	}
	if kind == "shopping" {
		err = a.shoppingAssignment(&e, r)
	}
	shops, peopleErr := a.store.Shops()
	p.Shops = shops
	if peopleErr != nil {
		a.fail(w, peopleErr)
		return
	}
	people, peopleErr := a.store.People()
	if peopleErr != nil {
		a.fail(w, peopleErr)
		return
	}
	p.People = people
	p.Entry = e
	a.formTitle(&p)
	if err == nil {
		err = Validate(e, a.loc)
	}
	if err != nil {
		p.Error = err.Error()
		w.Header().Set("HX-Retarget", "#drawer")
		w.Header().Set("HX-Reswap", "innerHTML")
		a.render(w, "form", p)
		return
	}
	if err = a.store.Save(e); err != nil {
		a.fail(w, err)
		return
	}
	if kind == "shopping" {
		u, _ := url.Parse(p.Return)
		if filter := u.Query().Get("store"); u.Path == "/shopping" && filter != "" && !matchesShop(e, filter) {
			p.Return = shoppingURL(e.ShopID)
		}
	}
	if kind == "chore" && !choreDue(e, p.Today) {
		p.Return = "/chores?schedules=all"
	}
	a.changed(w, r, p)
}
func (a *App) complete(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = returnPath(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	done := r.FormValue("done")
	if done != "1" && done != "0" {
		http.Error(w, "Invalid completion state", 400)
		return
	}
	if err = a.store.SetDoneAt(id, p.Today, done == "1", p.Now); err != nil {
		a.fail(w, err)
		return
	}
	a.changed(w, r, p)
}
func (a *App) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err = a.store.Delete(id); err != nil {
		a.fail(w, err)
		return
	}
	p := a.base(w, r)
	p.Return = returnPath(r)
	a.changed(w, r, p)
}
func (a *App) clearShopping(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = returnPath(r)
	u, _ := url.Parse(p.Return)
	if u.Path != "/shopping" {
		http.Error(w, "Open the shopping list and try again.", 400)
		return
	}
	shops, err := a.store.Shops()
	if err != nil {
		a.fail(w, err)
		return
	}
	filter, err := shopFilter(u.Query().Get("store"), shops)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = a.store.ClearShoppingFilter(filter); err != nil {
		a.fail(w, err)
		return
	}
	a.changed(w, r, p)
}
func (a *App) resetDemo(w http.ResponseWriter, r *http.Request) {
	if err := a.store.ClearDemo(); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	p := a.base(w, r)
	p.Return = "/"
	a.changed(w, r, p)
}
func (a *App) changed(w http.ResponseWriter, r *http.Request, p Page) {
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, p.Return, http.StatusSeeOther)
		return
	}
	u, _ := url.Parse(p.Return)
	p.View = strings.Trim(u.Path, "/")
	if p.View == "" {
		p.View = "today"
	}
	if err := a.load(&p, u.Query()); err != nil {
		a.fail(w, err)
		return
	}
	if current, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil && current.RequestURI() != p.Return {
		w.Header().Set("HX-Push-Url", p.Return)
	}
	w.Header().Set("HX-Trigger", "dashboardChanged")
	a.render(w, "fragment", p)
}
func (a *App) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotDue) {
		http.Error(w, "This chore is not scheduled for today. Reload the list.", 409)
	} else if errors.Is(err, ErrMissing) {
		http.Error(w, "This item no longer exists. Reload the dashboard.", 404)
	} else {
		log.Print(err)
		http.Error(w, "Unable to save. Try again.", 500)
	}
}
