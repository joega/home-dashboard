package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	imgcolor "image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testRecipe() Recipe {
	return Recipe{Title: "Family dinner", Meal: "Dinner", Servings: "4 servings", Ingredients: []string{"2 cups rice", "Salt, to taste"}, Steps: []string{"Boil water.", "Cook the rice."}, Active: true, Favorite: true, PrepMinutes: 5, CookMinutes: 20}
}
func TestRecipeStorageConflictAndAtomicShopping(t *testing.T) {
	s, path := testStore(t)
	r := testRecipe()
	r.CreateKey = "one-form"
	id, err := s.SaveRecipe(r)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SaveRecipe(r)
	if err != nil || again != id {
		t.Fatal("duplicate form created a second recipe")
	}
	r, err = s.Recipe(id)
	if err != nil || !r.Active || !r.Favorite || r.Version != 1 {
		t.Fatal(r, err)
	}
	old := r
	r.Notes = "Paper cookbook notes"
	if _, err = s.SaveRecipe(r); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveRecipe(old); err != ErrRecipeChanged {
		t.Fatal("stale edit overwrote recipe", err)
	}
	r, _ = s.Recipe(id)
	token := strings.Repeat("a", 48)
	items := []RecipeShoppingItem{{Index: 0, Name: "rice", Quantity: "2 cups"}, {Index: 1, Name: "salt"}}
	if n, e := s.AddRecipeShopping(id, r.Version, token, items); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	if n, e := s.AddRecipeShopping(id, r.Version, token, items); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	list, _ := s.List("shopping", "2026-10-13")
	if len(list) != 2 {
		t.Fatal("repeated transfer duplicated items", list)
	}
	items[1].StoreID = 999
	if _, e := s.AddRecipeShopping(id, r.Version, strings.Repeat("b", 48), items); e == nil {
		t.Fatal("invalid store accepted")
	}
	list, _ = s.List("shopping", "2026-10-13")
	if len(list) != 2 {
		t.Fatal("failed transfer partially committed")
	}
	if _, e := s.AddRecipeShopping(id, 1, strings.Repeat("c", 48), items[:1]); e != ErrRecipeChanged {
		t.Fatal(e)
	}
	if e := s.RecipeAction(id, "active", false); e != nil {
		t.Fatal(e)
	}
	all, _ := s.Recipes()
	if len(filterRecipes(all, RecipeFilter{})) != 0 || len(filterRecipes(all, RecipeFilter{Archived: true})) != 1 {
		t.Fatal("archive filtering failed")
	}
	s.Close()
	s, err = Open(path, false, "2026-10-13")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err = s.Recipe(id)
	if err != nil || r.Notes != "Paper cookbook notes" {
		t.Fatal("recipe did not persist", r, err)
	}
}
func TestRecipeSchemaImportAndNetworkGuards(t *testing.T) {
	raw := []byte(`<html><script type="application/ld+json">{"@graph":[{"@type":["Recipe","Thing"],"name":"Dinner &amp; rice","recipeIngredient":["2 cups rice","salt"],"recipeInstructions":[{"@type":"HowToSection","name":"Part 1","itemListElement":[{"@type":"HowToStep","text":"<p>1. Boil water.</p>"},{"@type":"HowToStep","text":"2. Cook rice."}]}],"prepTime":"PT5M","cookTime":"PT20M","recipeYield":"4 servings","image":{"url":"/meal.jpg"},"author":{"name":"A cook"}}]}</script></html>`)
	recipes, err := schemaRecipes(raw, "https://recipes.example/dinner")
	if err != nil || len(recipes) != 1 {
		t.Fatal(recipes, err)
	}
	r := recipes[0]
	if r.Title != "Dinner & rice" || r.Minutes() != 25 || len(r.Steps) != 2 || r.Steps[0] != "Boil water." || r.PhotoURL != "https://recipes.example/meal.jpg" {
		t.Fatal(r)
	}
	for _, u := range []string{"file:///etc/passwd", "http://127.0.0.1/", "http://192.168.50.10/", "http://[::1]/", "https://user:pass@example.com/", "http://localhost/", "http://foo.local/", "http://example.com:8080/"} {
		if _, e := recipeURL(u); e == nil {
			t.Fatal("unsafe URL accepted", u)
		}
	}
	for _, v := range []string{"127.0.0.1", "::ffff:192.168.50.10", "10.0.0.1", "169.254.169.254", "100.64.1.1", "0.0.0.2", "64:ff9b::c0a8:0101"} {
		if publicRecipeIP(netip.MustParseAddr(v)) {
			t.Fatal("private address accepted", v)
		}
	}
	if !publicRecipeIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
	client := recipeHTTPClient()
	request := httptest.NewRequest("GET", "http://127.0.0.1/test", nil)
	if client.CheckRedirect(request, nil) == nil {
		t.Fatal("redirect into LAN accepted")
	}
	if _, e := schemaRecipes([]byte(`<p>No recipe metadata</p>`), "https://example.com/"); e == nil {
		t.Fatal("missing recipe accepted")
	}
}
func TestRecipeDraftPrivacyPhotosAndFilters(t *testing.T) {
	s, _ := testStore(t)
	r := testRecipe()
	key, err := s.PutRecipeDraft(r, "owner-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, e := s.RecipeDraft(key, "owner-two"); e != ErrMissing {
		t.Fatal("another browser read draft")
	}
	all := []Recipe{r, {Title: "Cake", Meal: "Dessert", Active: true, Favorite: true}, {Title: "Archived", Meal: "Dinner", Active: false}}
	if len(filterRecipes(all, RecipeFilter{Favorite: true, Meal: "Dinner", MaxMinutes: 30})) != 1 {
		t.Fatal("filters failed")
	}
	im := image.NewRGBA(image.Rect(0, 0, 1200, 600))
	im.Set(0, 0, imgcolor.White)
	var raw bytes.Buffer
	png.Encode(&raw, im)
	photo, err := recipePhoto(&raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg, kind, e := image.DecodeConfig(bytes.NewReader(photo))
	if e != nil || kind != "jpeg" || cfg.Width != 640 || cfg.Height != 320 {
		t.Fatal("photo was not normalized", cfg, kind, e)
	}
}

type recipeRoundTrip func(*http.Request) (*http.Response, error)

func (f recipeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRecipeHTTPImportEditCookAndShopping(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	a, err := New(s, loc)
	if err != nil {
		t.Fatal(err)
	}
	doc := `{"@type":"Recipe","name":"Imported dinner","recipeIngredient":["2 cups rice"],"recipeInstructions":["Boil water.","Cook rice."],"recipeYield":"4 servings"}`
	a.recipeClient = &http.Client{Transport: recipeRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(doc)), Header: http.Header{}, Request: r}, nil
	})}
	get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://household.test"+path, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	first := get("/cookbook", nil)
	if first.Code != 200 || !strings.Contains(first.Body.String(), "Your favorites belong here.") {
		t.Fatal(first.Body.String())
	}
	cookie := first.Result().Cookies()[0]
	post := func(path string, v url.Values) *httptest.ResponseRecorder {
		v.Set("csrf", cookie.Value)
		r := httptest.NewRequest("POST", "http://household.test"+path, strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	w := post("/cookbook/import", url.Values{"url": {"https://recipes.example/dinner"}})
	dest := recipeLocation(w)
	if !strings.HasPrefix(dest, "/cookbook/drafts/") {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	key := strings.Split(dest, "/")[3]
	preview := get(dest, cookie)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), "Review imported recipe") {
		t.Fatal(preview.Body.String())
	}
	values := url.Values{"draft": {key}, "create_key": {key}, "title": {"Imported dinner"}, "meal": {"Dinner"}, "servings": {"4 servings"}, "ingredients": {"2 cups rice"}, "steps": {"Boil water.\nCook rice."}, "favorite": {"on"}, "source": {"https://recipes.example/dinner"}}

	values.Set("prep", "1.5")
	bad := post("/cookbook/recipes/0", values)
	if !strings.Contains(bad.Body.String(), "Times must be whole minutes.") {
		t.Fatal("invalid number accepted")
	}
	values.Del("prep")
	w = post("/cookbook/recipes/0", values)
	target := recipeLocation(w)
	if !strings.Contains(target, "/cookbook/recipes/1") {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	if get("/cookbook/recipes/1/cook", cookie).Code != 200 {
		t.Fatal("cooking view failed")
	}
	if get("/cookbook/recipes/1/shopping", cookie).Code != 200 {
		t.Fatal("ingredient selection failed")
	}
	export := get("/cookbook/recipes/1/export", cookie)
	var data map[string]any
	if json.Unmarshal(export.Body.Bytes(), &data) != nil || data["@type"] != "Recipe" {
		t.Fatal("export invalid")
	}
	picked := get("/cookbook/pick?favorite=1", cookie)
	if picked.Code != 303 || !strings.Contains(picked.Header().Get("Location"), "/cookbook/recipes/1") {
		t.Fatal("picker ignored saved dinner filters", picked.Code, picked.Header())
	}
	recipe, _ := s.Recipe(1)
	if !recipe.Favorite || recipe.Version != 1 {
		t.Fatal(recipe)
	}
	w = post("/cookbook/recipes/1/shopping", url.Values{"version": {"1"}, "batch": {strings.Repeat("d", 48)}, "need": {"0"}, "name_0": {"rice"}, "quantity_0": {"2 cups"}, "store_0": {"0"}})
	if recipeLocation(w) != "/shopping?recipe_added=1" {
		t.Fatal(w.Body.String(), w.Header())
	}

	bad = post("/cookbook/recipes/1/shopping", url.Values{"version": {"1"}, "batch": {strings.Repeat("e", 48)}, "name_0": {"Household rice"}, "quantity_0": {"3 cups"}, "store_0": {"0"}})
	if !strings.Contains(bad.Body.String(), `value="Household rice"`) || !strings.Contains(bad.Body.String(), `data-shopping-retry`) || strings.Contains(bad.Body.String(), `value="0" checked aria-label="Add`) {
		t.Fatal("failed shopping form lost edits or choices", bad.Body.String())
	}
	filtered := get("/cookbook?meal=Dinner&favorite=1", cookie).Body.String()
	if !strings.Contains(filtered, `href="/cookbook/pick?favorite=1&amp;meal=Dinner"`) {
		t.Fatal("filtered picker URL incorrectly escaped", filtered)
	}
	cooked := post("/cookbook/recipes/1/cooked", url.Values{})
	if !strings.Contains(cooked.Header().Get("HX-Trigger"), `"key":"1-1"`) || recipeLocation(cooked) != "/cookbook/recipes/1?cooked=1" {
		t.Fatal("completion did not clear browser progress before navigation", cooked.Header())
	}
	// A cached source import is inert text: script-like recipe content is escaped in the editor and detail.
	recipe.Title = `<script>alert(1)</script>`
	recipe.Version = 1
	if _, err = s.SaveRecipe(recipe); err != nil {
		t.Fatal(err)
	}
	body := get("/cookbook/recipes/1", cookie).Body.String()
	if strings.Contains(body, recipe.Title) || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("recipe title was not escaped")
	}
	if _, err = s.RecipeDraft(key, recipeOwner(cookie.Value)); err != ErrMissing {
		t.Fatal("saved preview remained live")
	}
}

func recipeLocation(w *httptest.ResponseRecorder) string {
	var data map[string]string
	json.Unmarshal([]byte(w.Header().Get("HX-Location")), &data)
	return data["path"]
}

func TestMealDBNormalizationCacheAndSavedIdentity(t *testing.T) {
	s, _ := testStore(t)
	loc, _ := time.LoadLocation("America/New_York")
	a, e := New(s, loc)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	a.recipeClient = &http.Client{Transport: recipeRoundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Query().Get("i") != "123" {
			t.Fatal(req.URL)
		}
		doc := `{"meals":[{"idMeal":"123","strMeal":"Family beans","strIngredient1":"beans","strMeasure1":"1 can","strInstructions":"STEP 1\nWarm beans.\nSTEP 2\nServe.","strCategory":"Vegetarian","strArea":"American"}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(doc)), Header: http.Header{}, Request: req}, nil
	})}
	r, e := a.mealDBRecipe(t.Context(), "123")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Steps) != 2 || r.Steps[0] != "Warm beans." || r.Meal != "Dinner" || r.Minutes() != 0 || r.Ingredients[0] != "1 can beans" {
		t.Fatal(r)
	}
	if _, e = a.mealDBRecipe(t.Context(), "123"); e != nil || calls != 1 {
		t.Fatal("provider cache missed", e, calls)
	}
	id, e := s.SaveRecipe(r)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.SaveRecipe(r)
	if e != nil || id != again {
		t.Fatal("provider save duplicated recipe", e)
	}
}

func TestDeleteRecipeAtomicAndScoped(t *testing.T) {
	s, _ := testStore(t)
	recipe := testRecipe()
	recipe.ProviderRef = "mealdb:delete-test"
	recipe.Photo = []byte("recipe photo")
	id, err := s.SaveRecipe(recipe)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.SaveRecipe(testRecipe())
	if err != nil {
		t.Fatal(err)
	}
	for i, rid := range []int64{id, other} {
		token := strings.Repeat(string(rune('a'+i)), 48)
		if _, err := s.AddRecipeShopping(rid, 1, token, []RecipeShoppingItem{{Index: 0, Name: "rice", Quantity: "2 cups"}}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(table, where string, args ...any) int {
		t.Helper()
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+where, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	shoppingBefore := count("entries", "kind='shopping'")
	if err := s.DeleteRecipe(id, 0); !errors.Is(err, ErrRecipeChanged) {
		t.Fatal("missing version accepted", err)
	}
	changed, _ := s.Recipe(id)
	changed.Notes = "New notes"
	if _, err := s.SaveRecipe(changed); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRecipe(id, 1); !errors.Is(err, ErrRecipeChanged) {
		t.Fatal("stale delete accepted", err)
	}
	if count("recipe_shopping_batches", "recipe_id=?", id) != 1 {
		t.Fatal("stale delete removed transfer record")
	}
	// A failure after removing the receipts must roll the entire operation back.
	if _, err := s.db.Exec("CREATE TRIGGER block_recipe_delete BEFORE DELETE ON recipes BEGIN SELECT RAISE(ABORT, 'test failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRecipe(id, 2); err == nil {
		t.Fatal("expected delete failure")
	}
	if count("recipes", "id=?", id) != 1 || count("recipe_shopping_batches", "recipe_id=?", id) != 1 {
		t.Fatal("failed delete partially committed")
	}
	if _, err := s.db.Exec("DROP TRIGGER block_recipe_delete"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecipeAction(id, "active", false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRecipe(id, 2); err != nil {
		t.Fatal("archived recipe delete failed", err)
	}
	if _, err := s.Recipe(id); !errors.Is(err, ErrMissing) {
		t.Fatal("recipe remains", err)
	}
	if count("recipe_shopping_batches", "recipe_id=?", id) != 0 {
		t.Fatal("transfer record remains")
	}
	if count("recipes", "id=?", other) != 1 || count("recipe_shopping_batches", "recipe_id=?", other) != 1 {
		t.Fatal("another recipe was affected")
	}
	if count("entries", "kind='shopping'") != shoppingBefore {
		t.Fatal("shopping items were removed")
	}
	if err := s.DeleteRecipe(id, 2); !errors.Is(err, ErrMissing) {
		t.Fatal("missing recipe delete", err)
	}
	replacement, err := s.SaveRecipe(recipe)
	if err != nil || replacement == id {
		t.Fatal("deleted import could not be replaced", replacement, err)
	}
}

func TestRecipeDeleteHTTP(t *testing.T) {
	s, _ := testStore(t)
	id, err := s.SaveRecipe(testRecipe())
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(s, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, values url.Values, cookie *http.Cookie, hx bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://household.test"+path, strings.NewReader(values.Encode()))
		if method == "POST" {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	base := recipePath(id)
	edit := request("GET", base+"/edit", nil, nil, false)
	cookie := edit.Result().Cookies()[0]
	if edit.Code != 200 || !strings.Contains(edit.Body.String(), `id="recipe-delete"`) || !strings.Contains(edit.Body.String(), "This cannot be undone.") {
		t.Fatal("delete not in existing editor", edit.Code)
	}
	if body := request("GET", "/cookbook/new", nil, cookie, false).Body.String(); strings.Contains(body, "Delete recipe") {
		t.Fatal("new recipe has delete action")
	}
	draft, err := s.PutRecipeDraft(testRecipe(), recipeOwner(cookie.Value))
	if err != nil {
		t.Fatal(err)
	}
	if body := request("GET", "/cookbook/drafts/"+draft+"/edit", nil, cookie, false).Body.String(); strings.Contains(body, "Delete recipe") {
		t.Fatal("import preview has delete action")
	}
	if w := request("GET", base+"/delete", nil, cookie, false); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatal("GET delete accepted", w.Code)
	}
	if w := request("POST", base+"/delete", url.Values{"version": {"1"}}, cookie, true); w.Code != http.StatusForbidden {
		t.Fatal("missing CSRF accepted", w.Code)
	}
	stale := request("POST", base+"/delete", url.Values{"version": {"0"}, "csrf": {cookie.Value}}, cookie, true)
	if !strings.Contains(stale.Body.String(), "Review it before deleting.") || !strings.Contains(stale.Header().Get("HX-Push-Url"), base+"/edit") {
		t.Fatal("stale delete did not return to editor", stale.Code, stale.Header())
	}
	if _, err := s.Recipe(id); err != nil {
		t.Fatal("rejected request deleted recipe", err)
	}
	w := request("POST", base+"/delete", url.Values{"version": {"1"}, "csrf": {cookie.Value}, "back": {"/cookbook?meal=Dinner&favorite=1"}}, cookie, true)
	if recipeLocation(w) != "/cookbook?deleted=1&favorite=1&meal=Dinner" {
		t.Fatal("wrong delete redirect", w.Header(), w.Body.String())
	}
	library := request("GET", recipeLocation(w), nil, cookie, false)
	if library.Code != 200 || !strings.Contains(library.Body.String(), "Recipe deleted.") {
		t.Fatal("missing delete feedback")
	}
	if w := request("GET", base, nil, cookie, false); w.Code != http.StatusNotFound {
		t.Fatal("deleted recipe still available", w.Code)
	}
	id, err = s.SaveRecipe(testRecipe())
	if err != nil {
		t.Fatal(err)
	}
	w = request("POST", recipePath(id)+"/delete", url.Values{"version": {"1"}, "csrf": {cookie.Value}, "back": {"https://example.com/elsewhere"}}, cookie, false)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/cookbook?deleted=1" {
		t.Fatal("unsafe fallback", w.Code, w.Header())
	}
}
