package dashboard

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SummaryGroup struct {
	Name, URL string
	Count     int
}

// Keep all-day events and timed events whose calendar duration has not ended.
// Entries are already expanded to the household date and ordered by start time.
func remainingTodayEvents(entries []Entry, now time.Time) []Entry {
	minute := now.Hour()*60 + now.Minute()
	remaining := make([]Entry, 0, len(entries))
	for _, event := range entries {
		if event.Start == "" || minute < endMinute(event) {
			remaining = append(remaining, event)
		}
	}
	return remaining
}

// Group only pending items; chores must also be due on this household date.
func pendingGroups(entries []Entry, chores bool) ([]SummaryGroup, int) {
	groups := map[int64]*SummaryGroup{}
	total := 0
	for _, e := range entries {
		if e.Done || (chores && !e.Due) {
			continue
		}
		id, name, href := e.ShopID, e.ShopName, shoppingURL(e.ShopID)
		if chores {
			id = e.PersonID
			name = e.Owner
			filter := "none"
			if id != 0 {
				filter = strconv.FormatInt(id, 10)
			}
			href = choreURL(filter, "", false)
		}
		if id == 0 {
			if chores {
				name = "Household"
			} else {
				name = "Any store"
			}
		}
		if groups[id] == nil {
			groups[id] = &SummaryGroup{Name: name, URL: href}
		}
		groups[id].Count++
		total++
	}
	ids := make([]int64, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i] == 0 || ids[j] == 0 {
			return ids[i] < ids[j]
		}
		a, b := strings.ToLower(groups[ids[i]].Name), strings.ToLower(groups[ids[j]].Name)
		if a == b {
			return ids[i] < ids[j]
		}
		return a < b
	})
	result := make([]SummaryGroup, 0, len(ids))
	for _, id := range ids {
		result = append(result, *groups[id])
	}
	return result, total
}
func chorePersonFilter(raw string, people []Person) string {
	if raw == "" || raw == "none" {
		return raw
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err == nil && id > 0 {
		for _, p := range people {
			if p.ID == id {
				return strconv.FormatInt(id, 10)
			}
		}
	}
	return ""
}
func matchesChorePerson(e Entry, filter string) bool {
	return filter == "" || (filter == "none" && e.PersonID == 0) || strconv.FormatInt(e.PersonID, 10) == filter
}
func choreURL(person, routine string, all bool) string {
	q := url.Values{}
	if person != "" {
		q.Set("person", person)
	}
	if routine != "" {
		q.Set("routine", routine)
	}
	if all {
		q.Set("schedules", "all")
	}
	if len(q) == 0 {
		return "/chores"
	}
	return "/chores?" + q.Encode()
}
