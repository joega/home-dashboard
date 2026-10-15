package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Recipe struct {
	ID             int64    `json:"id"`
	CreateKey      string   `json:"-"`
	ProviderRef    string   `json:"provider_ref,omitempty"`
	Version        int      `json:"version"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Meal           string   `json:"meal"`
	Servings       string   `json:"servings"`
	PrepMinutes    int      `json:"prep_minutes"`
	CookMinutes    int      `json:"cook_minutes"`
	TotalMinutes   int      `json:"total_minutes"`
	Ingredients    []string `json:"ingredients"`
	Steps          []string `json:"steps"`
	Tags           []string `json:"tags"`
	Source         string   `json:"source"`
	Author         string   `json:"author"`
	PhotoURL       string   `json:"photo_url,omitempty"`
	Notes          string   `json:"notes"`
	Favorite       bool     `json:"favorite"`
	Active         bool     `json:"active"`
	LastCooked     string   `json:"last_cooked,omitempty"`
	Photo          []byte   `json:"-"`
	HasPhoto       bool     `json:"-"`
	Warning        string   `json:"import_warning,omitempty"`
	PhotoAttempted bool     `json:"photo_attempted,omitempty"`
}

func (r Recipe) Minutes() int {
	if r.TotalMinutes > 0 {
		return r.TotalMinutes
	}
	return r.PrepMinutes + r.CookMinutes
}
func (r Recipe) TimeLabel() string {
	n := r.Minutes()
	if n == 0 {
		return "Time not set"
	}
	if n < 60 {
		return fmt.Sprintf("%d min", n)
	}
	if n%60 == 0 {
		return fmt.Sprintf("%d hr", n/60)
	}
	return fmt.Sprintf("%d hr %d min", n/60, n%60)
}

var recipeMeals = []string{"Dinner", "Lunch", "Breakfast", "Snack", "Dessert", "Other"}

func textBound(s string, n int) bool {
	return utf8.RuneCountInString(s) <= n && !strings.ContainsRune(s, 0)
}
func (r *Recipe) normalize() {
	r.Title = strings.TrimSpace(r.Title)
	r.Description = strings.TrimSpace(r.Description)
	r.Notes = strings.TrimSpace(r.Notes)
	r.Ingredients = cleanLines(r.Ingredients)
	r.Steps = cleanLines(r.Steps)
	r.Tags = cleanLines(r.Tags)
	if r.Meal == "" {
		r.Meal = "Dinner"
	}
	if r.Servings == "" {
		r.Servings = "Not specified"
	}
}
func cleanLines(lines []string) []string {
	var out []string
	for _, s := range lines {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
func (r Recipe) Validate() error {
	if strings.TrimSpace(r.Title) == "" || !textBound(r.Title, 120) {
		return errors.New("Enter a recipe title up to 120 characters.")
	}
	valid := false
	for _, m := range recipeMeals {
		if r.Meal == m {
			valid = true
		}
	}
	if !valid {
		return errors.New("Choose a meal type.")
	}
	if !textBound(r.Description, 1000) || !textBound(r.Notes, 4000) || !textBound(r.Author, 160) || !textBound(r.Servings, 80) {
		return errors.New("The description, notes, author or servings are too long.")
	}
	if len(r.Ingredients) == 0 || len(r.Ingredients) > 80 || len(r.Steps) == 0 || len(r.Steps) > 80 {
		return errors.New("Add 1–80 ingredients and 1–80 steps.")
	}
	for _, v := range r.Ingredients {
		if !textBound(v, 240) {
			return errors.New("An ingredient is longer than 240 characters.")
		}
	}
	for _, v := range r.Steps {
		if !textBound(v, 1500) {
			return errors.New("A step is longer than 1,500 characters.")
		}
	}
	if len(r.Tags) > 12 {
		return errors.New("Use at most 12 tags.")
	}
	for _, v := range r.Tags {
		if !textBound(v, 40) {
			return errors.New("Keep each tag under 40 characters.")
		}
	}
	for _, n := range []int{r.PrepMinutes, r.CookMinutes, r.TotalMinutes} {
		if n < 0 || n > 10080 {
			return errors.New("Times must be between 0 and 10,080 minutes.")
		}
	}
	for _, link := range []string{r.Source, r.PhotoURL} {
		if link != "" {
			if _, err := recipeURL(link); err != nil {
				return errors.New("Source and photo links must be public HTTP or HTTPS URLs.")
			}
		}
	}
	return nil
}
func (s *Store) migrateRecipes() error {
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS recipes(id INTEGER PRIMARY KEY AUTOINCREMENT,version INTEGER NOT NULL DEFAULT 1,title TEXT NOT NULL,body TEXT NOT NULL,provider_ref TEXT NOT NULL DEFAULT '',create_key TEXT NOT NULL DEFAULT '',photo BLOB,active INTEGER NOT NULL DEFAULT 1,favorite INTEGER NOT NULL DEFAULT 0,last_cooked TEXT NOT NULL DEFAULT '')`,
		`CREATE UNIQUE INDEX IF NOT EXISTS recipes_provider ON recipes(provider_ref) WHERE provider_ref<>''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS recipes_create_key ON recipes(create_key) WHERE create_key<>''`,
		`CREATE TABLE IF NOT EXISTS recipe_cache(key TEXT PRIMARY KEY,body BLOB NOT NULL,expires_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS recipe_drafts(token TEXT PRIMARY KEY,owner TEXT NOT NULL,body TEXT NOT NULL,photo BLOB,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS recipe_shopping_batches(token TEXT PRIMARY KEY,recipe_id INTEGER NOT NULL REFERENCES recipes(id),digest TEXT NOT NULL,added INTEGER NOT NULL,created_at TEXT NOT NULL)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
func decodeRecipe(id int64, version int, body string, photo []byte, has, active, favorite bool, last string) (Recipe, error) {
	var r Recipe
	err := json.Unmarshal([]byte(body), &r)
	r.ID = id
	r.Version = version
	r.Photo = photo
	r.HasPhoto = has
	r.Active = active
	r.Favorite = favorite
	r.LastCooked = last
	return r, err
}
func (s *Store) Recipe(id int64) (Recipe, error) {
	var body, last string
	var photo []byte
	var version int
	var active, favorite bool
	err := s.db.QueryRow("SELECT version,body,photo,active,favorite,last_cooked FROM recipes WHERE id=?", id).Scan(&version, &body, &photo, &active, &favorite, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return Recipe{}, ErrMissing
	}
	if err != nil {
		return Recipe{}, err
	}
	return decodeRecipe(id, version, body, photo, len(photo) > 0, active, favorite, last)
}
func (s *Store) Recipes() ([]Recipe, error) {
	rows, err := s.db.Query("SELECT id,version,body,COALESCE(length(photo),0)>0,active,favorite,last_cooked FROM recipes ORDER BY title COLLATE NOCASE,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Recipe
	for rows.Next() {
		var id int64
		var version int
		var body, last string
		var has, active, favorite bool
		if err = rows.Scan(&id, &version, &body, &has, &active, &favorite, &last); err != nil {
			return nil, err
		}
		r, e := decodeRecipe(id, version, body, nil, has, active, favorite, last)
		if e != nil {
			return nil, e
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

var ErrRecipeChanged = errors.New("This recipe changed in another browser. Reload it before saving or adding ingredients.")

func (s *Store) SaveRecipe(r Recipe) (int64, error) {
	r.normalize()
	if err := r.Validate(); err != nil {
		return 0, err
	}
	body, _ := json.Marshal(r)
	if r.ID == 0 {
		if r.CreateKey != "" {
			var existing int64
			if e := s.db.QueryRow("SELECT id FROM recipes WHERE create_key=?", r.CreateKey).Scan(&existing); e == nil {
				return existing, nil
			}
		}
		if r.ProviderRef != "" {
			if id, e := s.RecipeByProvider(r.ProviderRef); e == nil {
				return id, nil
			}
		}
		result, err := s.db.Exec("INSERT INTO recipes(title,body,provider_ref,create_key,photo,active,favorite) VALUES(?,?,?,?,?,1,?)", r.Title, string(body), r.ProviderRef, r.CreateKey, r.Photo, r.Favorite)
		if err != nil {
			if r.CreateKey != "" {
				var existing int64
				if e := s.db.QueryRow("SELECT id FROM recipes WHERE create_key=?", r.CreateKey).Scan(&existing); e == nil {
					return existing, nil
				}
			}
			if r.ProviderRef != "" {
				if id, e := s.RecipeByProvider(r.ProviderRef); e == nil {
					return id, nil
				}
			}
			return 0, err
		}
		return result.LastInsertId()
	}
	result, err := s.db.Exec("UPDATE recipes SET title=?,body=?,photo=CASE WHEN ? THEN ? ELSE photo END,favorite=?,version=version+1 WHERE id=? AND version=?", r.Title, string(body), r.Photo != nil, r.Photo, r.Favorite, r.ID, r.Version)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return 0, ErrRecipeChanged
	}
	return r.ID, nil
}

// DeleteRecipe removes the saved recipe and transfer receipts, leaving shopping items intact.
func (s *Store) DeleteRecipe(id int64, version int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err = tx.QueryRow("SELECT version FROM recipes WHERE id=?", id).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return ErrMissing
	} else if err != nil {
		return err
	}
	if version != current {
		return ErrRecipeChanged
	}
	if _, err = tx.Exec("DELETE FROM recipe_shopping_batches WHERE recipe_id=?", id); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM recipes WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecipeAction(id int64, action string, value bool) error {
	q := ""
	var arg any = value
	switch action {
	case "favorite":
		q = "UPDATE recipes SET favorite=? WHERE id=?"
	case "active":
		q = "UPDATE recipes SET active=? WHERE id=?"
	case "cooked":
		q = "UPDATE recipes SET last_cooked=? WHERE id=? AND active=1"
		arg = time.Now().UTC().Format(time.RFC3339)
	default:
		return errors.New("Unknown recipe action")
	}
	res, err := s.db.Exec(q, arg, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrMissing
	}
	return nil
}
func recipeOwner(csrf string) string {
	d := sha256.Sum256([]byte(csrf))
	return hex.EncodeToString(d[:])
}
func randomToken() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *Store) PutRecipeDraft(r Recipe, owner string) (string, error) {
	if _, err := s.db.Exec("DELETE FROM recipe_drafts WHERE created_at<?", time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339)); err != nil {
		return "", err
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM recipe_drafts").Scan(&n)
	if n >= 100 {
		return "", errors.New("Too many import previews. Try again tomorrow.")
	}
	key := randomToken()
	raw, _ := json.Marshal(r)
	_, err := s.db.Exec("INSERT INTO recipe_drafts(token,owner,body,photo,created_at) VALUES(?,?,?,?,?)", key, owner, string(raw), r.Photo, time.Now().UTC().Format(time.RFC3339))
	return key, err
}
func (s *Store) RecipeDraft(key, owner string) (Recipe, error) {
	var raw, created string
	var photo []byte
	err := s.db.QueryRow("SELECT body,photo,created_at FROM recipe_drafts WHERE token=? AND owner=?", key, owner).Scan(&raw, &photo, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Recipe{}, ErrMissing
	}
	if err != nil {
		return Recipe{}, err
	}
	at, e := time.Parse(time.RFC3339, created)
	if e != nil || time.Since(at) > 24*time.Hour {
		return Recipe{}, ErrMissing
	}
	var r Recipe
	err = json.Unmarshal([]byte(raw), &r)
	r.Photo = photo
	r.HasPhoto = len(photo) > 0
	return r, err
}
func (s *Store) RemoveRecipeDraft(key, owner string) {
	s.db.Exec("DELETE FROM recipe_drafts WHERE token=? AND owner=?", key, owner)
}

type RecipeFilter struct {
	Search, Meal, Tag  string
	Favorite, Archived bool
	MaxMinutes         int
}

func recipeFilter(q url.Values) RecipeFilter {
	f := RecipeFilter{Search: strings.TrimSpace(q.Get("q")), Meal: q.Get("meal"), Tag: q.Get("tag"), Favorite: q.Get("favorite") == "1", Archived: q.Get("archived") == "1"}
	if len(f.Search) > 160 {
		f.Search = f.Search[:160]
	}
	f.MaxMinutes, _ = strconv.Atoi(q.Get("max"))
	if f.MaxMinutes < 0 || f.MaxMinutes > 10080 {
		f.MaxMinutes = 0
	}
	return f
}
func (f RecipeFilter) URL() string {
	q := url.Values{}
	if f.Search != "" {
		q.Set("q", f.Search)
	}
	if f.Meal != "" {
		q.Set("meal", f.Meal)
	}
	if f.Tag != "" {
		q.Set("tag", f.Tag)
	}
	if f.Favorite {
		q.Set("favorite", "1")
	}
	if f.Archived {
		q.Set("archived", "1")
	}
	if f.MaxMinutes > 0 {
		q.Set("max", strconv.Itoa(f.MaxMinutes))
	}
	if len(q) == 0 {
		return "/cookbook"
	}
	return "/cookbook?" + q.Encode()
}
func filterRecipes(all []Recipe, f RecipeFilter) []Recipe {
	var result []Recipe
	for _, r := range all {
		if r.Active == f.Archived || f.Favorite && !r.Favorite || f.Meal != "" && r.Meal != f.Meal || f.MaxMinutes > 0 && (r.Minutes() == 0 || r.Minutes() > f.MaxMinutes) {
			continue
		}
		if f.Tag != "" {
			found := false
			for _, tag := range r.Tags {
				if tag == f.Tag {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		hay := r.Title + " " + r.Description + " " + strings.Join(r.Tags, " ") + " " + strings.Join(r.Ingredients, " ")
		if f.Search != "" && !strings.Contains(strings.ToLower(hay), strings.ToLower(f.Search)) {
			continue
		}
		result = append(result, r)
	}
	return result
}
func recipeTags(all []Recipe) []string {
	set := map[string]bool{}
	for _, r := range all {
		for _, tag := range r.Tags {
			set[tag] = true
		}
	}
	var tags []string
	for tag := range set {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

type RecipeShoppingItem struct {
	Index          int
	Name, Quantity string
	StoreID        int64
	Selected       bool `json:"-"`
}

func (s *Store) AddRecipeShopping(id int64, version int, token string, items []RecipeShoppingItem) (int, error) {
	return s.AddRecipeShoppingAs(id, version, token, items, Person{}, time.Now())
}
func (s *Store) AddRecipeShoppingAs(id int64, version int, token string, items []RecipeShoppingItem, actor Person, at time.Time) (int, error) {
	if len(token) != 48 || len(items) == 0 || len(items) > 80 {
		return 0, errors.New("Choose the ingredients you need.")
	}
	for _, c := range token {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return 0, errors.New("Invalid shopping batch.")
		}
	}
	raw, _ := json.Marshal(items)
	d := sha256.Sum256(append([]byte(fmt.Sprintf("%d:%d:", id, version)), raw...))
	digest := hex.EncodeToString(d[:])
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var oldDigest string
	var added int
	err = tx.QueryRow("SELECT digest,added FROM recipe_shopping_batches WHERE token=?", token).Scan(&oldDigest, &added)
	if err == nil {
		if oldDigest != digest {
			return 0, errors.New("This ingredient form was already submitted. Open a fresh form to add more.")
		}
		return added, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var current int
	var active bool
	var body string
	err = tx.QueryRow("SELECT version,active,body FROM recipes WHERE id=?", id).Scan(&current, &active, &body)
	if err != nil {
		return 0, err
	}
	if !active || current != version {
		return 0, ErrRecipeChanged
	}
	var recipe Recipe
	if err = json.Unmarshal([]byte(body), &recipe); err != nil {
		return 0, err
	}
	seen := map[int]bool{}
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		quantity := strings.TrimSpace(item.Quantity)
		if item.Index < 0 || item.Index >= len(recipe.Ingredients) || seen[item.Index] || name == "" || !textBound(name, 120) || !textBound(quantity, 80) || item.StoreID < 0 {
			return 0, errors.New("Check the selected ingredients, names and quantities.")
		}
		seen[item.Index] = true
		if item.StoreID > 0 {
			var enabled bool
			if e := tx.QueryRow("SELECT active FROM shopping_stores WHERE id=?", item.StoreID).Scan(&enabled); e != nil || !enabled {
				return 0, errors.New("Choose an active store for each ingredient.")
			}
		}
		var duplicate int
		if err = tx.QueryRow("SELECT COUNT(*) FROM entries WHERE kind='shopping' AND done=0 AND title=? COLLATE NOCASE AND quantity=? COLLATE NOCASE AND COALESCE(shop_id,0)=?", name, quantity, item.StoreID).Scan(&duplicate); err != nil {
			return 0, err
		}
		if duplicate > 0 {
			continue
		}
		actor, err = actorInTransaction(tx, actor)
		if err != nil {
			return 0, err
		}
		stamp := at.UTC().Format(time.RFC3339Nano)
		result, e := tx.Exec("INSERT INTO entries(kind,title,quantity,shop_id,entry_key,created_by,created_by_name,created_at) VALUES('shopping',?,?,?,lower(hex(randomblob(16))),?,?,?)", name, quantity, personRef(item.StoreID), personRef(actor.ID), actor.Name, stamp)
		if err = e; err != nil {
			return 0, err
		}
		entryID, e := result.LastInsertId()
		if e != nil {
			return 0, e
		}
		if e = recordEntryActivity(tx, entryID, "added", actor, stamp, ""); e != nil {
			return 0, e
		}
		added++
	}
	if _, err = tx.Exec("INSERT INTO recipe_shopping_batches(token,recipe_id,digest,added,created_at) VALUES(?,?,?,?,?)", token, id, digest, added, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return 0, err
	}
	return added, tx.Commit()
}

func (s *Store) RecipeByProvider(ref string) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT id FROM recipes WHERE provider_ref=?", ref).Scan(&id)
	return id, err
}
func (s *Store) recipeCache(key string) ([]byte, error) {
	var b []byte
	var expires string
	err := s.db.QueryRow("SELECT body,expires_at FROM recipe_cache WHERE key=?", key).Scan(&b, &expires)
	if err != nil {
		return nil, err
	}
	at, e := time.Parse(time.RFC3339, expires)
	if e != nil || time.Now().After(at) {
		return nil, ErrMissing
	}
	return b, nil
}
func (s *Store) putRecipeCache(key string, b []byte) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM recipe_cache WHERE expires_at<?", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO recipe_cache(key,body,expires_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body,expires_at=excluded.expires_at", key, b, time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM recipe_cache WHERE key IN (SELECT key FROM recipe_cache ORDER BY expires_at DESC,key LIMIT -1 OFFSET 300)"); err != nil {
		return err
	}
	return tx.Commit()
}
