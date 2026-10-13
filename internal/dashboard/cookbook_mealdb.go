package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Provider normalization, bounded saved photos and ingredient-selection flow adapt
// Kitchen Frame, Copyright (c) 2026 Hunter Yeagley, MIT. See THIRD_PARTY_NOTICES.md.
var mealID = regexp.MustCompile(`^[0-9]{1,10}$`)

func (a *App) mealDBJSON(ctx context.Context, path string, out any) error {
	key := os.Getenv("MEALDB_API_KEY")
	if key == "" {
		key = "1"
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`).MatchString(key) {
		return errors.New("The recipe provider key is invalid")
	}
	cacheKey := "mdb:" + path
	b, err := a.store.recipeCache(cacheKey)
	if err != nil {
		b, _, err = fetchRecipeBytes(ctx, a.recipeClient, "https://www.themealdb.com/api/json/v1/"+key+"/"+path, 2<<20)
		if err != nil {
			return errors.New("TheMealDB is unavailable. Your saved cookbook is still available.")
		}
		if !json.Valid(b) {
			return errors.New("The recipe provider returned invalid data")
		}
		if err = a.store.putRecipeCache(cacheKey, b); err != nil {
			return err
		}
	}
	return json.Unmarshal(b, out)
}
func mealDBRecipe(m map[string]string) Recipe {
	r := Recipe{Title: m["strMeal"], ProviderRef: "mdb:" + m["idMeal"], PhotoURL: m["strMealThumb"], Source: m["strSource"], Author: "TheMealDB", Meal: "Dinner", Servings: "Not specified", Active: true}
	if _, err := recipeURL(r.Source); err != nil {
		r.Source = "https://www.themealdb.com/"
	}
	for i := 1; i <= 20; i++ {
		suffix := itoa(i)
		name := strings.TrimSpace(m["strIngredient"+suffix])
		amount := strings.TrimSpace(m["strMeasure"+suffix])
		if name != "" {
			r.Ingredients = append(r.Ingredients, strings.TrimSpace(amount+" "+name))
		}
	}
	r.Steps = recipeSteps(m["strInstructions"])
	switch m["strCategory"] {
	case "Breakfast":
		r.Meal = "Breakfast"
	case "Dessert":
		r.Meal = "Dessert"
	case "Starter", "Side":
		r.Meal = "Other"
	}
	for _, tag := range strings.Split(m["strTags"], ",") {
		if strings.TrimSpace(tag) != "" {
			r.Tags = append(r.Tags, strings.TrimSpace(tag))
		}
	}
	for _, tag := range []string{m["strCategory"], m["strArea"]} {
		if tag != "" {
			r.Tags = append(r.Tags, tag)
		}
	}
	r.normalize()
	return r
}
func (a *App) mealDBRecipe(ctx context.Context, id string) (Recipe, error) {
	if !mealID.MatchString(id) {
		return Recipe{}, ErrMissing
	}
	var d struct {
		Meals []map[string]string `json:"meals"`
	}
	if err := a.mealDBJSON(ctx, "lookup.php?i="+id, &d); err != nil {
		return Recipe{}, err
	}
	if len(d.Meals) != 1 || d.Meals[0]["idMeal"] != id {
		return Recipe{}, ErrMissing
	}
	return mealDBRecipe(d.Meals[0]), nil
}
func (a *App) mealDBList(ctx context.Context, q, category string) ([]Recipe, []string, error) {
	var cats struct {
		Categories []struct {
			Category string `json:"strCategory"`
		} `json:"categories"`
	}
	if err := a.mealDBJSON(ctx, "categories.php", &cats); err != nil {
		return nil, nil, err
	}
	var categories []string
	for _, c := range cats.Categories {
		categories = append(categories, c.Category)
	}
	if category == "" {
		category = "Chicken"
	}
	found := false
	for _, c := range categories {
		if c == category {
			found = true
		}
	}
	if !found {
		category = "Chicken"
	}
	path := "filter.php?c=" + url.QueryEscape(category)
	if q = strings.TrimSpace(q); q != "" {
		if len(q) > 80 {
			q = q[:80]
		}
		path = "search.php?s=" + url.QueryEscape(q)
	}
	var d struct {
		Meals []map[string]string `json:"meals"`
	}
	if err := a.mealDBJSON(ctx, path, &d); err != nil {
		return nil, categories, err
	}
	var result []Recipe
	for _, m := range d.Meals {
		if !mealID.MatchString(m["idMeal"]) {
			continue
		}
		r := mealDBRecipe(m)
		result = append(result, r)
		meta, _ := json.Marshal(r)
		a.store.putRecipeCache("mdb:summary:"+m["idMeal"], meta)
	}
	return result, categories, nil
}
func (a *App) mealDBPhoto(ctx context.Context, id string) ([]byte, error) {
	if !mealID.MatchString(id) {
		return nil, ErrMissing
	}
	key := "mdb:photo:" + id
	if b, err := a.store.recipeCache(key); err == nil {
		return b, nil
	}
	var r Recipe
	if raw, err := a.store.recipeCache("mdb:summary:" + id); err == nil {
		json.Unmarshal(raw, &r)
	}
	if r.PhotoURL == "" {
		var err error
		r, err = a.mealDBRecipe(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	u, err := recipeURL(r.PhotoURL)
	if err != nil || !strings.EqualFold(u.Hostname(), "www.themealdb.com") {
		return nil, ErrMissing
	}
	raw, _, err := fetchRecipeBytes(ctx, a.recipeClient, r.PhotoURL, 3<<20)
	if err != nil {
		return nil, err
	}
	b, err := recipePhoto(bytes.NewReader(raw))
	if err == nil {
		err = a.store.putRecipeCache(key, b)
	}
	return b, err
}
