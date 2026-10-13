package dashboard

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Shop struct {
	ID     int64
	Name   string
	Active bool
	Items  int
}
type ShopRow struct {
	Shop Shop
	CSRF string
}

func (s *Store) migrateShops() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS shopping_stores(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT NOT NULL COLLATE NOCASE UNIQUE,active INTEGER NOT NULL DEFAULT 1)`); err != nil {
		return err
	}
	rows, err := tx.Query("PRAGMA table_info(entries)")
	if err != nil {
		return err
	}
	hasColumn := false
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "shop_id" {
			hasColumn = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !hasColumn {
		if _, err = tx.Exec("ALTER TABLE entries ADD COLUMN shop_id INTEGER REFERENCES shopping_stores(id)"); err != nil {
			return err
		}
	}
	var initialized int
	if err = tx.QueryRow("SELECT COUNT(*) FROM settings WHERE key='shopping_stores_v1'").Scan(&initialized); err != nil {
		return err
	}
	if initialized == 0 {
		for _, name := range []string{"Stop & Shop", "Target", "Costco"} {
			if _, err = tx.Exec("INSERT INTO shopping_stores(name) VALUES(?) ON CONFLICT(name) DO NOTHING", name); err != nil {
				return err
			}
		}
		if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES('shopping_stores_v1','1')"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Shops() ([]Shop, error) {
	rows, err := s.db.Query(`SELECT id,name,active,(SELECT COUNT(*) FROM entries WHERE kind='shopping' AND entries.shop_id=shopping_stores.id) FROM shopping_stores ORDER BY active DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var shops []Shop
	for rows.Next() {
		var shop Shop
		if err = rows.Scan(&shop.ID, &shop.Name, &shop.Active, &shop.Items); err != nil {
			return nil, err
		}
		shops = append(shops, shop)
	}
	return shops, rows.Err()
}
func (s *Store) Shop(id int64) (Shop, error) {
	var shop Shop
	err := s.db.QueryRow("SELECT id,name,active FROM shopping_stores WHERE id=?", id).Scan(&shop.ID, &shop.Name, &shop.Active)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrMissing
	}
	return shop, err
}
func (s *Store) SaveShop(shop Shop) error {
	shop.Name = strings.TrimSpace(shop.Name)
	if len([]rune(shop.Name)) < 1 || len([]rune(shop.Name)) > 60 {
		return errors.New("Enter a store name of 1–60 characters.")
	}
	var duplicate int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM shopping_stores WHERE name=? COLLATE NOCASE AND id<>?", shop.Name, shop.ID).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate > 0 {
		return errors.New("That store is already in Settings. Use a different name.")
	}
	if shop.ID == 0 {
		_, err := s.db.Exec("INSERT INTO shopping_stores(name) VALUES(?)", shop.Name)
		return err
	}
	result, err := s.db.Exec("UPDATE shopping_stores SET name=? WHERE id=?", shop.Name, shop.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrMissing
	}
	return err
}
func (s *Store) SetShopActive(id int64, active bool) error {
	result, err := s.db.Exec("UPDATE shopping_stores SET active=? WHERE id=?", active, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrMissing
	}
	return err
}
func shopFilter(raw string, shops []Shop) (string, error) {
	if raw == "" || raw == "none" {
		return raw, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err == nil && id > 0 {
		for _, shop := range shops {
			if shop.ID == id {
				return strconv.FormatInt(id, 10), nil
			}
		}
	}
	return "", errors.New("That store filter is unavailable. Open All items and try again.")
}
func matchesShop(e Entry, filter string) bool {
	if filter == "" {
		return true
	}
	if filter == "none" {
		return e.ShopID == 0
	}
	return strconv.FormatInt(e.ShopID, 10) == filter
}
func shoppingURL(id int64) string {
	if id == 0 {
		return "/shopping?store=none"
	}
	return "/shopping?store=" + strconv.FormatInt(id, 10)
}
func (s *Store) ClearShoppingFilter(filter string) error {
	if filter == "" {
		return s.ClearShopping()
	}
	if filter == "none" {
		_, err := s.db.Exec("DELETE FROM entries WHERE kind='shopping' AND done=1 AND shop_id IS NULL")
		return err
	}
	id, err := strconv.ParseInt(filter, 10, 64)
	if err != nil || id <= 0 {
		return errors.New("Invalid store filter")
	}
	_, err = s.db.Exec("DELETE FROM entries WHERE kind='shopping' AND done=1 AND shop_id=?", id)
	return err
}
func (a *App) shopForm(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = "/settings?section=stores"
	p.Shop = Shop{Active: true}
	p.FormTitle = "Add store"
	if r.PathValue("id") != "new" {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p.Shop, err = a.store.Shop(id)
		if err != nil {
			a.fail(w, err)
			return
		}
		p.FormTitle = "Edit store"
	}
	a.render(w, "shop-form", p)
}
func (a *App) saveShop(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.Return = "/settings?section=stores"
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 0 {
		http.NotFound(w, r)
		return
	}
	p.Shop = Shop{ID: id, Name: strings.TrimSpace(r.FormValue("name")), Active: true}
	p.FormTitle = "Add store"
	if id != 0 {
		old, err := a.store.Shop(id)
		if err != nil {
			a.fail(w, err)
			return
		}
		p.Shop.Active = old.Active
		p.FormTitle = "Edit store"
	}
	if err = a.store.SaveShop(p.Shop); err != nil {
		if errors.Is(err, ErrMissing) {
			a.fail(w, err)
			return
		}
		p.Error = err.Error()
		w.Header().Set("HX-Retarget", "#drawer")
		w.Header().Set("HX-Reswap", "innerHTML")
		a.render(w, "shop-form", p)
		return
	}
	a.changed(w, r, p)
}
func (a *App) shopStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	active := r.FormValue("active")
	if active != "0" && active != "1" {
		http.Error(w, "Invalid store status", 400)
		return
	}
	if err = a.store.SetShopActive(id, active == "1"); err != nil {
		a.fail(w, err)
		return
	}
	p := a.base(w, r)
	p.Return = "/settings?section=stores"
	a.changed(w, r, p)
}
func (a *App) shoppingAssignment(e *Entry, r *http.Request) error {
	raw := r.FormValue("store_id")
	if raw == "" {
		raw = "0"
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return errors.New("Choose a store from the list.")
	}
	e.ShopID = id
	if id == 0 {
		return nil
	}
	shop, err := a.store.Shop(id)
	if err != nil {
		return errors.New("That store is no longer available. Choose another store.")
	}
	if !shop.Active {
		old, err := a.store.Get(e.ID, time.Now().In(a.loc).Format("2006-01-02"))
		if err != nil || old.ShopID != id {
			return errors.New("Choose an active store from the list.")
		}
	}
	e.ShopName = shop.Name
	return nil
}
