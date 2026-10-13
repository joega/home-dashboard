package dashboard

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

func itoa(n int) string          { return strconv.Itoa(n) }
func recipePath(id int64) string { return "/cookbook/recipes/" + strconv.FormatInt(id, 10) }
func recipeID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 0 {
		return 0, ErrMissing
	}
	return id, nil
}
func cookbookReturn(raw string) string {
	u, err := url.Parse(raw)
	if err == nil && u.Host == "" && u.Scheme == "" && u.Path == "/cookbook" {
		return u.RequestURI()
	}
	return "/cookbook"
}
func recipePickURL(back string) string {
	u, _ := url.Parse(cookbookReturn(back))
	u.Path = "/cookbook/pick"
	return u.String()
}
func (a *App) registerCookbook(mux *http.ServeMux) {
	mux.HandleFunc("GET /cookbook", a.cookbookLibrary)
	mux.HandleFunc("GET /cookbook/new", a.recipeEditor)
	mux.HandleFunc("GET /cookbook/import", a.recipeImportForm)
	mux.HandleFunc("POST /cookbook/import", a.recipeImport)
	mux.HandleFunc("GET /cookbook/pick", a.recipePick)
	mux.HandleFunc("GET /cookbook/explore", a.recipeExplore)
	mux.HandleFunc("GET /cookbook/explore/{meal}", a.recipeExploreDetail)
	mux.HandleFunc("GET /cookbook/explore/{meal}/photo", a.recipeExplorePhoto)
	mux.HandleFunc("POST /cookbook/explore/{meal}/import", a.recipeExploreImport)
	mux.HandleFunc("GET /cookbook/drafts/{token}/edit", a.recipeDraftEditor)
	mux.HandleFunc("GET /cookbook/drafts/{token}/photo", a.recipeDraftPhoto)
	mux.HandleFunc("GET /cookbook/recipes/{id}", a.recipeDetail)
	mux.HandleFunc("GET /cookbook/recipes/{id}/edit", a.recipeEditor)
	mux.HandleFunc("GET /cookbook/recipes/{id}/cook", a.recipeCook)
	mux.HandleFunc("GET /cookbook/recipes/{id}/photo", a.recipeImage)
	mux.HandleFunc("GET /cookbook/recipes/{id}/export", a.recipeExport)
	mux.HandleFunc("POST /cookbook/recipes/{id}", a.recipeSave)
	mux.HandleFunc("POST /cookbook/recipes/{id}/favorite", a.recipeFavorite)
	mux.HandleFunc("POST /cookbook/recipes/{id}/active", a.recipeActive)
	mux.HandleFunc("POST /cookbook/recipes/{id}/cooked", a.recipeCooked)
	mux.HandleFunc("GET /cookbook/recipes/{id}/shopping", a.recipeShopping)
	mux.HandleFunc("POST /cookbook/recipes/{id}/shopping", a.recipeAddShopping)
}
func (a *App) cookbookPage(w http.ResponseWriter, r *http.Request, mode string) Page {
	p := a.base(w, r)
	p.View = "cookbook"
	p.Title = "Cookbook"
	p.CookbookMode = mode
	p.RecipeReturn = cookbookReturn(r.FormValue("back"))
	p.Meals = recipeMeals
	if err := a.load(&p, url.Values{}); err != nil {
		p.Error = "The dashboard data could not be loaded."
	}
	return p
}
func (a *App) cookbookRender(w http.ResponseWriter, r *http.Request, p Page) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Push-Url", r.URL.RequestURI())
		a.render(w, "fragment", p)
	} else {
		a.render(w, "page", p)
	}
}
func (a *App) cookbookRedirect(w http.ResponseWriter, r *http.Request, target string) {
	if r.Header.Get("HX-Request") == "true" {
		location, _ := json.Marshal(map[string]string{"path": target, "target": "#content", "swap": "innerHTML"})
		w.Header().Set("HX-Location", string(location))
		w.WriteHeader(200)
	} else {
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
}
func (a *App) cookbookLibrary(w http.ResponseWriter, r *http.Request) {
	p := a.cookbookPage(w, r, "library")
	all, err := a.store.Recipes()
	if err != nil {
		a.fail(w, err)
		return
	}
	p.RecipeFilter = recipeFilter(r.URL.Query())
	p.RecipeReturn = p.RecipeFilter.URL()
	p.RecipeTags = recipeTags(all)
	filtered := filterRecipes(all, p.RecipeFilter)
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Favorite && !filtered[j].Favorite })
	p.RecipeCount = len(filtered)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(1, page)
	last := max(1, (len(filtered)+11)/12)
	page = min(page, last)
	start := (page - 1) * 12
	p.Recipes = filtered[start:min(start+12, len(filtered))]
	pageURL := func(n int) string {
		u, _ := url.Parse(p.RecipeReturn)
		q := u.Query()
		q.Set("page", itoa(n))
		u.RawQuery = q.Encode()
		return u.String()
	}
	if page > 1 {
		p.RecipePrev = pageURL(page - 1)
	}
	if page < last {
		p.RecipeNext = pageURL(page + 1)
	}
	if r.URL.Query().Get("empty_pick") == "1" {
		p.Notice = "No saved dinner recipes match these filters. Add a dinner recipe or adjust your filters."
	}
	a.cookbookRender(w, r, p)
}
func (a *App) recipeEditor(w http.ResponseWriter, r *http.Request) {
	p := a.cookbookPage(w, r, "editor")
	p.Title = "Add recipe"
	p.RecipeCreateKey = randomToken()
	p.Recipe = Recipe{Meal: "Dinner", Servings: "4 servings", Active: true}
	if r.PathValue("id") != "" {
		id, err := recipeID(r)
		if err != nil {
			a.fail(w, err)
			return
		}
		p.Recipe, err = a.store.Recipe(id)
		if err != nil {
			a.fail(w, err)
			return
		}
		p.Title = "Edit recipe"
		p.Recipe.Photo = nil
		p.RecipeImage = recipePath(id) + "/photo?v=" + itoa(p.Recipe.Version)
	}
	a.cookbookRender(w, r, p)
}
func (a *App) recipeImportForm(w http.ResponseWriter, r *http.Request) {
	p := a.cookbookPage(w, r, "import")
	p.Title = "Import recipe"
	a.cookbookRender(w, r, p)
}
func (a *App) recipeImport(w http.ResponseWriter, r *http.Request) {
	p := a.cookbookPage(w, r, "import")
	p.Title = "Import recipe"
	var raw []byte
	source := strings.TrimSpace(r.Form.Get("url"))
	var err error
	if source != "" {
		raw, _, err = fetchRecipeBytes(r.Context(), a.recipeClient, source, 2<<20)
	} else if f, _, e := r.FormFile("recipe_file"); e == nil {
		defer f.Close()
		raw, err = io.ReadAll(io.LimitReader(f, (2<<20)+1))
		if len(raw) > 2<<20 {
			err = errors.New("Recipe files must be smaller than 2 MB.")
		}
	} else {
		err = errors.New("Paste a recipe link or choose a JSON-LD file.")
	}
	var recipes []Recipe
	if err == nil {
		recipes, err = schemaRecipes(raw, source)
	}
	if err != nil {
		p.Error = err.Error()
		a.cookbookRender(w, r, p)
		return
	}
	var keys []string
	for _, recipe := range recipes {
		key, e := a.store.PutRecipeDraft(recipe, recipeOwner(p.CSRF))
		if e != nil {
			a.fail(w, e)
			return
		}
		keys = append(keys, key)
	}
	if len(keys) == 1 {
		a.cookbookRedirect(w, r, "/cookbook/drafts/"+keys[0]+"/edit")
		return
	}
	p.CookbookMode = "choices"
	p.Title = "Choose a recipe"
	p.Recipes = recipes
	p.DraftKeys = keys
	a.cookbookRender(w, r, p)
}
func (a *App) recipeDraftEditor(w http.ResponseWriter, r *http.Request) {
	p := a.cookbookPage(w, r, "editor")
	p.Title = "Review imported recipe"
	p.DraftKey = r.PathValue("token")
	p.RecipeCreateKey = p.DraftKey
	var err error
	p.Recipe, err = a.store.RecipeDraft(p.DraftKey, recipeOwner(p.CSRF))
	if err != nil {
		a.fail(w, err)
		return
	}
	if p.Recipe.PhotoURL != "" && !p.Recipe.PhotoAttempted {
		p.Recipe.PhotoAttempted = true
		p.Recipe.Photo, err = loadRecipePhoto(r.Context(), a.recipeClient, p.Recipe.PhotoURL)
		if err != nil {
			p.Recipe.Warning = "The recipe was imported, but its photo could not be copied. You can upload a JPG or PNG."
			p.Recipe.Photo = nil
		}
		if strings.HasPrefix(p.Recipe.PhotoURL, "data:") {
			p.Recipe.PhotoURL = ""
		}
		p.Recipe.HasPhoto = len(p.Recipe.Photo) > 0
		raw, _ := json.Marshal(p.Recipe)
		a.store.db.Exec("UPDATE recipe_drafts SET body=?,photo=? WHERE token=? AND owner=?", string(raw), p.Recipe.Photo, p.DraftKey, recipeOwner(p.CSRF))
	}
	p.RecipeImage = "/cookbook/drafts/" + p.DraftKey + "/photo"
	a.cookbookRender(w, r, p)
}
func (a *App) recipeSave(w http.ResponseWriter, r *http.Request) {
	p := a.cookbookPage(w, r, "editor")
	id, err := recipeID(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	p.Title = "Add recipe"
	var old Recipe
	if id > 0 {
		old, err = a.store.Recipe(id)
		if err != nil {
			a.fail(w, err)
			return
		}
		p.Title = "Edit recipe"
		p.RecipeImage = recipePath(id) + "/photo?v=" + itoa(old.Version)
	}
	p.DraftKey = r.Form.Get("draft")
	p.RecipeCreateKey = r.Form.Get("create_key")
	var draft Recipe
	if p.DraftKey != "" {
		draft, err = a.store.RecipeDraft(p.DraftKey, recipeOwner(p.CSRF))
		if err != nil {
			p.Error = "The import preview expired. Import it again."
			a.cookbookRender(w, r, p)
			return
		}
	}
	var numberErr error
	n := func(key string) int {
		raw := strings.TrimSpace(r.Form.Get(key))
		if raw == "" {
			return 0
		}
		x, e := strconv.Atoi(raw)
		if e != nil {
			numberErr = errors.New("Times must be whole minutes.")
		}
		return x
	}
	recipe := Recipe{ID: id, Version: n("version"), ProviderRef: old.ProviderRef, Title: r.Form.Get("title"), Description: r.Form.Get("description"), Meal: r.Form.Get("meal"), Servings: r.Form.Get("servings"), PrepMinutes: n("prep"), CookMinutes: n("cook"), TotalMinutes: n("total"), Ingredients: strings.Split(strings.ReplaceAll(r.Form.Get("ingredients"), "\r\n", "\n"), "\n"), Steps: strings.Split(strings.ReplaceAll(r.Form.Get("steps"), "\r\n", "\n"), "\n"), Tags: strings.Split(r.Form.Get("tags"), ","), Source: strings.TrimSpace(r.Form.Get("source")), Author: r.Form.Get("author"), Notes: r.Form.Get("notes"), Favorite: r.Form.Get("favorite") == "on", PhotoURL: strings.TrimSpace(r.Form.Get("photo_url")), Active: true}
	if p.DraftKey != "" {
		recipe.ProviderRef = draft.ProviderRef
		recipe.Photo = draft.Photo
		recipe.HasPhoto = draft.HasPhoto
	}
	recipe.normalize()
	if id == 0 {
		if len(p.RecipeCreateKey) != 48 || strings.Trim(p.RecipeCreateKey, "0123456789abcdef") != "" {
			p.Error = "Reload the editor before saving."
			p.Recipe = recipe
			a.cookbookRender(w, r, p)
			return
		}
		recipe.CreateKey = recipeOwner(p.CSRF) + ":" + p.RecipeCreateKey
	}
	err = recipe.Validate()
	if numberErr != nil {
		err = numberErr
	}
	uploaded := false
	if err == nil {
		if f, _, e := r.FormFile("photo"); e == nil {
			defer f.Close()
			recipe.Photo, err = recipePhoto(f)
			uploaded = true
		} else if e != http.ErrMissingFile && e != http.ErrNotMultipart {
			err = errors.New("The photo could not be uploaded.")
		}
	}
	if err == nil && r.Form.Get("remove_photo") == "on" {
		recipe.Photo = []byte{}
		recipe.PhotoURL = ""
	}
	if err == nil && !uploaded && r.Form.Get("remove_photo") != "on" && recipe.PhotoURL != "" && recipe.PhotoURL != old.PhotoURL && recipe.PhotoURL != draft.PhotoURL {
		recipe.Photo, err = loadRecipePhoto(r.Context(), a.recipeClient, recipe.PhotoURL)
	}
	p.Recipe = recipe
	if p.DraftKey != "" {
		p.RecipeImage = "/cookbook/drafts/" + p.DraftKey + "/photo"
	}
	p.Recipe.HasPhoto = old.HasPhoto || len(recipe.Photo) > 0
	if err == nil {
		id, err = a.store.SaveRecipe(recipe)
	}
	if err != nil {
		p.Error = err.Error()
		if f := r.Form.Get("favorite"); f != "" {
			p.Recipe.Favorite = true
		}
		a.cookbookRender(w, r, p)
		return
	}
	if p.DraftKey != "" {
		a.store.RemoveRecipeDraft(p.DraftKey, recipeOwner(p.CSRF))
	}
	a.cookbookRedirect(w, r, recipePath(id)+"?saved=1")
}
func (a *App) recipeDetailPage(w http.ResponseWriter, r *http.Request, mode string) {
	p := a.cookbookPage(w, r, mode)
	id, err := recipeID(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	p.Recipe, err = a.store.Recipe(id)
	if err != nil {
		a.fail(w, err)
		return
	}
	p.Title = p.Recipe.Title
	p.RecipeBase = recipePath(id)
	p.RecipeImage = p.RecipeBase + "/photo?v=" + itoa(p.Recipe.Version)
	if mode == "cook" && !p.Recipe.Active {
		p.Error = "Restore this recipe before cooking it."
		p.CookbookMode = "detail"
	}
	if r.URL.Query().Get("saved") == "1" {
		p.Notice = "Recipe saved."
	}
	if r.URL.Query().Get("picked") == "1" {
		p.Notice = "Tonight’s dinner pick."
	}
	if r.URL.Query().Get("cooked") == "1" {
		p.Notice = "Marked as cooked."
	}
	a.cookbookRender(w, r, p)
}
func (a *App) recipeDetail(w http.ResponseWriter, r *http.Request) {
	a.recipeDetailPage(w, r, "detail")
}
func (a *App) recipeCook(w http.ResponseWriter, r *http.Request)     { a.recipeDetailPage(w, r, "cook") }
func (a *App) recipeFavorite(w http.ResponseWriter, r *http.Request) { a.recipeState(w, r, "favorite") }
func (a *App) recipeActive(w http.ResponseWriter, r *http.Request)   { a.recipeState(w, r, "active") }
func (a *App) recipeState(w http.ResponseWriter, r *http.Request, action string) {
	id, err := recipeID(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	value := r.Form.Get("value")
	if value != "0" && value != "1" {
		http.Error(w, "Invalid value", 400)
		return
	}
	if err = a.store.RecipeAction(id, action, value == "1"); err != nil {
		a.fail(w, err)
		return
	}
	target := cookbookReturn(r.Form.Get("back"))
	if r.Form.Get("detail") == "1" {
		target = recipePath(id)
	}
	a.cookbookRedirect(w, r, target)
}
func (a *App) recipeCooked(w http.ResponseWriter, r *http.Request) {
	id, err := recipeID(r)
	var recipe Recipe
	if err == nil {
		recipe, err = a.store.Recipe(id)
	}
	if err == nil {
		err = a.store.RecipeAction(id, "cooked", true)
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	// HTMX follows HX-Location before afterRequest can identify a removed form.
	// Emit completion first so this browser clears its checklist before navigation.
	trigger, _ := json.Marshal(map[string]any{"recipeCooked": map[string]string{"key": itoa(int(id)) + "-" + itoa(recipe.Version)}})
	w.Header().Set("HX-Trigger", string(trigger))
	a.cookbookRedirect(w, r, recipePath(id)+"?cooked=1")
}
func (a *App) recipePick(w http.ResponseWriter, r *http.Request) {
	all, err := a.store.Recipes()
	if err != nil {
		a.fail(w, err)
		return
	}
	f := recipeFilter(r.URL.Query())
	var choices []Recipe
	for _, recipe := range filterRecipes(all, f) {
		if recipe.Active && recipe.Meal == "Dinner" {
			choices = append(choices, recipe)
		}
	}
	if len(choices) == 0 {
		u, _ := url.Parse(f.URL())
		q := u.Query()
		q.Set("empty_pick", "1")
		u.RawQuery = q.Encode()
		a.cookbookRedirect(w, r, u.String())
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(choices))))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.cookbookRedirect(w, r, recipePath(choices[n.Int64()].ID)+"?picked=1&back="+url.QueryEscape(f.URL()))
}
func serveRecipePhoto(w http.ResponseWriter, b []byte) {
	if len(b) == 0 {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Length", itoa(len(b)))
	w.Write(b)
}
func (a *App) recipeImage(w http.ResponseWriter, r *http.Request) {
	id, err := recipeID(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	recipe, err := a.store.Recipe(id)
	if err != nil {
		a.fail(w, err)
		return
	}
	serveRecipePhoto(w, recipe.Photo)
}
func (a *App) recipeDraftPhoto(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("hq_csrf")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	recipe, err := a.store.RecipeDraft(r.PathValue("token"), recipeOwner(cookie.Value))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveRecipePhoto(w, recipe.Photo)
}
func (a *App) recipeExport(w http.ResponseWriter, r *http.Request) {
	id, err := recipeID(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	recipe, err := a.store.Recipe(id)
	if err != nil {
		a.fail(w, err)
		return
	}
	steps := []any{}
	for _, s := range recipe.Steps {
		steps = append(steps, map[string]string{"@type": "HowToStep", "text": s})
	}
	data := map[string]any{"@context": "https://schema.org", "@type": "Recipe", "name": recipe.Title, "description": recipe.Description, "recipeIngredient": recipe.Ingredients, "recipeInstructions": steps, "recipeYield": recipe.Servings, "recipeCategory": recipe.Meal, "keywords": strings.Join(recipe.Tags, ","), "author": map[string]string{"@type": "Person", "name": recipe.Author}, "url": recipe.Source, "householdNotes": recipe.Notes}
	if recipe.PrepMinutes > 0 {
		data["prepTime"] = "PT" + itoa(recipe.PrepMinutes) + "M"
	}
	if recipe.CookMinutes > 0 {
		data["cookTime"] = "PT" + itoa(recipe.CookMinutes) + "M"
	}
	if recipe.Minutes() > 0 {
		data["totalTime"] = "PT" + itoa(recipe.Minutes()) + "M"
	}
	if len(recipe.Photo) > 0 {
		data["image"] = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(recipe.Photo)
	}
	w.Header().Set("Content-Type", "application/ld+json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="recipe-%d.jsonld"`, id))
	json.NewEncoder(w).Encode(data)
}
func (a *App) recipeShopping(w http.ResponseWriter, r *http.Request) {
	p := a.cookbookPage(w, r, "shopping")
	id, err := recipeID(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	p.Recipe, err = a.store.Recipe(id)
	if err != nil {
		a.fail(w, err)
		return
	}
	p.Title = "Ingredients for Shopping"
	p.RecipeBase = recipePath(id)
	p.BatchToken = randomToken()
	for i, line := range p.Recipe.Ingredients {
		name, quantity := splitRecipeIngredient(line)
		p.RecipeShopping = append(p.RecipeShopping, RecipeShoppingItem{Index: i, Name: name, Quantity: quantity, Selected: true})
	}
	a.cookbookRender(w, r, p)
}
func (a *App) recipeAddShopping(w http.ResponseWriter, r *http.Request) {
	id, err := recipeID(r)
	if err != nil {
		a.fail(w, err)
		return
	}
	version, _ := strconv.Atoi(r.Form.Get("version"))
	var items []RecipeShoppingItem
	for _, v := range r.Form["need"] {
		i, e := strconv.Atoi(v)
		if e != nil {
			http.Error(w, "Invalid ingredient", 400)
			return
		}
		storeID, e := strconv.ParseInt(r.Form.Get("store_"+itoa(i)), 10, 64)
		if e != nil {
			storeID = -1
		}
		items = append(items, RecipeShoppingItem{Index: i, Name: r.Form.Get("name_" + itoa(i)), Quantity: r.Form.Get("quantity_" + itoa(i)), StoreID: storeID})
	}
	n, err := a.store.AddRecipeShopping(id, version, r.Form.Get("batch"), items)
	if err != nil {
		p := a.cookbookPage(w, r, "shopping")
		p.Recipe, _ = a.store.Recipe(id)
		p.Title = "Ingredients for Shopping"
		p.RecipeBase = recipePath(id)
		p.Error = err.Error()
		p.BatchToken = r.Form.Get("batch")
		p.ShoppingRetry = true
		selected := map[int]bool{}
		for _, item := range items {
			selected[item.Index] = true
		}
		for i := range p.Recipe.Ingredients {
			storeID, _ := strconv.ParseInt(r.Form.Get("store_"+itoa(i)), 10, 64)
			p.RecipeShopping = append(p.RecipeShopping, RecipeShoppingItem{Index: i, Name: r.Form.Get("name_" + itoa(i)), Quantity: r.Form.Get("quantity_" + itoa(i)), StoreID: storeID, Selected: selected[i]})
		}
		a.cookbookRender(w, r, p)
		return
	}
	a.cookbookRedirect(w, r, "/shopping?recipe_added="+itoa(n))
}
func (a *App) recipeExplore(w http.ResponseWriter, r *http.Request) {
	p := a.cookbookPage(w, r, "explore")
	p.ExploreCategory = r.URL.Query().Get("category")
	if p.ExploreCategory == "" {
		p.ExploreCategory = "Chicken"
	}
	p.RecipeFilter.Search = r.URL.Query().Get("q")
	all, cats, err := a.mealDBList(r.Context(), p.RecipeFilter.Search, p.ExploreCategory)
	p.RecipeCategories = cats
	if err != nil {
		p.Error = err.Error()
	} else {
		p.RecipeCount = len(all)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		page = max(1, page)
		last := max(1, (len(all)+11)/12)
		page = min(page, last)
		start := (page - 1) * 12
		p.Recipes = all[start:min(start+12, len(all))]
		path := func(n int) string {
			return "/cookbook/explore?category=" + url.QueryEscape(p.ExploreCategory) + "&q=" + url.QueryEscape(p.RecipeFilter.Search) + "&page=" + itoa(n)
		}
		if page > 1 {
			p.RecipePrev = path(page - 1)
		}
		if page < last {
			p.RecipeNext = path(page + 1)
		}
	}
	a.cookbookRender(w, r, p)
}
func (a *App) recipeExploreDetail(w http.ResponseWriter, r *http.Request) {
	p := a.cookbookPage(w, r, "detail")
	recipe, err := a.mealDBRecipe(r.Context(), r.PathValue("meal"))
	if err != nil {
		a.fail(w, err)
		return
	}
	p.Recipe = recipe
	p.Title = recipe.Title
	p.RecipeBase = "/cookbook/explore/" + r.PathValue("meal")
	p.RecipeImage = p.RecipeBase + "/photo"
	p.ExplorePreview = true
	p.RecipeReturn = "/cookbook/explore"
	if id, e := a.store.RecipeByProvider(recipe.ProviderRef); e == nil {
		p.ExistingRecipe = id
	}
	a.cookbookRender(w, r, p)
}
func (a *App) recipeExplorePhoto(w http.ResponseWriter, r *http.Request) {
	b, err := a.mealDBPhoto(r.Context(), r.PathValue("meal"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveRecipePhoto(w, b)
}
func (a *App) recipeExploreImport(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	recipe, err := a.mealDBRecipe(r.Context(), r.PathValue("meal"))
	if err != nil {
		a.fail(w, err)
		return
	}
	if id, e := a.store.RecipeByProvider(recipe.ProviderRef); e == nil {
		a.cookbookRedirect(w, r, recipePath(id))
		return
	}
	if recipe.Photo, err = a.mealDBPhoto(r.Context(), r.PathValue("meal")); err != nil {
		recipe.Warning = "This recipe photo could not be copied. You can upload your own."
	}
	recipe.PhotoAttempted = true
	recipe.HasPhoto = len(recipe.Photo) > 0
	key, err := a.store.PutRecipeDraft(recipe, recipeOwner(p.CSRF))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.cookbookRedirect(w, r, "/cookbook/drafts/"+key+"/edit")
}

type RecipeCard struct {
	Recipe                 Recipe
	URL, Image, CSRF, Back string
	Explore                bool
}

func makeRecipeCard(r Recipe, p Page) RecipeCard {
	c := RecipeCard{Recipe: r, CSRF: p.CSRF, Back: p.RecipeReturn, URL: recipePath(r.ID)}
	if r.HasPhoto {
		c.Image = c.URL + "/photo?v=" + itoa(r.Version)
	}
	if p.CookbookMode == "explore" {
		c.Explore = true
		c.URL = "/cookbook/explore/" + strings.TrimPrefix(r.ProviderRef, "mdb:")
		c.Image = c.URL + "/photo"
	}
	if !c.Explore {
		c.URL += "?back=" + url.QueryEscape(p.RecipeReturn)
	}
	return c
}
