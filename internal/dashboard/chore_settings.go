package dashboard

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

func (s *Store) migrateChoreSettings() error {
	var present int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('people') WHERE name='chore_eligible'").Scan(&present); err != nil {
		return err
	}
	if present == 0 {
		_, err := s.db.Exec("ALTER TABLE people ADD COLUMN chore_eligible INTEGER NOT NULL DEFAULT 1 CHECK(chore_eligible IN (0,1))")
		return err
	}
	return nil
}

func chorePeople(people []Person) []Person {
	var result []Person
	for _, person := range people {
		if person.ChoreEligible {
			result = append(result, person)
		}
	}
	return result
}

// Display excluded assignees as shared chores without rewriting their saved assignments.
func householdChoreAssignments(entries []Entry, people []Person) []Entry {
	excluded := map[int64]bool{}
	for _, person := range people {
		if !person.ChoreEligible {
			excluded[person.ID] = true
		}
	}
	result := append([]Entry(nil), entries...)
	for i := range result {
		if result[i].PersonID == 0 || excluded[result[i].PersonID] {
			result[i].PersonID = 0
			result[i].Owner = "Household"
			result[i].HasPhoto = false
		}
	}
	return result
}

// Only update the roster shown in the form. People added in another browser keep their default.
func (s *Store) SaveChorePeople(roster, eligible []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	selected := map[int64]bool{}
	for _, id := range eligible {
		selected[id] = true
	}
	seen := map[int64]bool{}
	for _, id := range roster {
		if id <= 0 || seen[id] {
			return errors.New("Reload Chores settings before saving.")
		}
		seen[id] = true
		var active bool
		if err := tx.QueryRow("SELECT active FROM people WHERE id=?", id).Scan(&active); err != nil || !active {
			return errors.New("The people list changed. Reload Chores settings before saving.")
		}
		if _, err := tx.Exec("UPDATE people SET chore_eligible=? WHERE id=?", selected[id], id); err != nil {
			return err
		}
	}
	for id := range selected {
		if !seen[id] {
			return errors.New("Choose people from the checklist.")
		}
	}
	return tx.Commit()
}

func (a *App) saveChoreSettings(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	p.View = "settings"
	if err := a.load(&p, url.Values{"section": {"chores"}}); err != nil {
		a.fail(w, err)
		return
	}
	parseIDs := func(key string) ([]int64, error) {
		var ids []int64
		for _, raw := range r.Form[key] {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return nil, errors.New("Choose people from the checklist.")
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	roster, err := parseIDs("person_id")
	if err == nil {
		var eligible []int64
		eligible, err = parseIDs("eligible")
		if err == nil {
			err = a.store.SaveChorePeople(roster, eligible)
		}
	}
	if err != nil {
		p.Error = err.Error()
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Push-Url", "/settings?section=chores")
			a.render(w, "fragment", p)
		} else {
			a.render(w, "page", p)
		}
		return
	}
	a.cookbookRedirect(w, r, "/settings?section=chores&saved=1")
}
