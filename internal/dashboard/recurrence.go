package dashboard

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func normalizeSchedule(e Entry) Entry {
	if e.Kind == "chore" && e.Repeat == "" && e.Daily {
		e.Repeat = "daily"
	}
	if e.RepeatEvery == 0 {
		e.RepeatEvery = 1
	}
	if e.Repeat == "" {
		e.RepeatEvery = 1
		e.RepeatDays = 0
		e.RepeatUntil = ""
	}
	if e.Repeat == "weekdays" {
		e.RepeatEvery = 1
	}
	e.Daily = e.Kind == "chore" && e.Repeat != ""
	return e
}
func (s *Store) migrateRecurrence() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("PRAGMA table_info(entries)")
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range []struct{ name, definition string }{
		{"repeat", "TEXT NOT NULL DEFAULT ''"}, {"repeat_every", "INTEGER NOT NULL DEFAULT 1"}, {"repeat_days", "INTEGER NOT NULL DEFAULT 0"}, {"repeat_until", "TEXT NOT NULL DEFAULT ''"}, {"location", "TEXT NOT NULL DEFAULT ''"},
	} {
		if !cols[column.name] {
			if _, err = tx.Exec("ALTER TABLE entries ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
	}
	var migrated int
	if err = tx.QueryRow("SELECT COUNT(*) FROM settings WHERE key='recurrence_v1'").Scan(&migrated); err != nil {
		return err
	}
	if migrated == 0 {
		if _, err = tx.Exec("UPDATE entries SET repeat='daily' WHERE kind='chore' AND daily=1"); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES('recurrence_v1','1')"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func calendarDate(value string) (time.Time, error) { return time.Parse("2006-01-02", value) }
func validSchedule(e Entry) error {
	if e.Kind == "shopping" {
		if e.Repeat != "" {
			return errors.New("Shopping items do not repeat.")
		}
		return nil
	}
	switch e.Repeat {
	case "", "daily", "weekdays", "weekly", "custom", "monthly", "yearly":
	default:
		return errors.New("Choose a valid repeat schedule.")
	}
	if e.RepeatEvery < 1 || e.RepeatEvery > 99 {
		return errors.New("Repeat every 1–99 days, weeks, months or years.")
	}
	if e.RepeatDays < 0 || e.RepeatDays > 127 {
		return errors.New("Choose valid days of the week.")
	}
	if e.Repeat == "custom" && e.RepeatDays == 0 {
		return errors.New("Select at least one day of the week.")
	}
	if e.Date != "" {
		if _, err := calendarDate(e.Date); err != nil {
			return errors.New("Choose a valid start date.")
		}
	}
	// Existing daily chores had no anchor date. They continue every day until edited.
	if e.Repeat != "" && e.Date == "" && !(e.Kind == "chore" && e.Repeat == "daily" && e.RepeatEvery == 1) {
		return errors.New("Choose when this schedule starts.")
	}
	if e.RepeatUntil != "" {
		if _, err := calendarDate(e.RepeatUntil); err != nil {
			return errors.New("Choose a valid end date.")
		}
		if e.Date != "" && e.RepeatUntil < e.Date {
			return errors.New("The end date must be on or after the start date.")
		}
	}
	return nil
}
func matchesDate(e Entry, day time.Time) bool {
	e = normalizeSchedule(e)
	date := day.Format("2006-01-02")
	if e.Repeat == "" {
		return e.Date == date
	}
	if e.RepeatUntil != "" && date > e.RepeatUntil {
		return false
	}
	if e.Date == "" {
		return e.Kind == "chore" && e.Repeat == "daily" && e.RepeatEvery == 1
	}
	start, err := calendarDate(e.Date)
	if err != nil || date < e.Date {
		return false
	}
	d, err := calendarDate(date)
	if err != nil {
		return false
	}
	delta := (d.Unix() - start.Unix()) / 86400
	every := int64(e.RepeatEvery)
	switch e.Repeat {
	case "daily":
		return delta%every == 0
	case "weekdays":
		return d.Weekday() != time.Saturday && d.Weekday() != time.Sunday
	case "weekly":
		return d.Weekday() == start.Weekday() && (delta/7)%every == 0
	case "custom":
		startMonday := start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
		dayMonday := d.AddDate(0, 0, -(int(d.Weekday())+6)%7)
		weeks := (dayMonday.Unix() - startMonday.Unix()) / (7 * 86400)
		return e.RepeatDays&(1<<int(d.Weekday())) != 0 && weeks%every == 0
	case "monthly":
		months := (d.Year()-start.Year())*12 + int(d.Month()) - int(start.Month())
		last := time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		return months%e.RepeatEvery == 0 && d.Day() == min(start.Day(), last)
	case "yearly":
		last := time.Date(d.Year(), start.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		return (d.Year()-start.Year())%e.RepeatEvery == 0 && d.Month() == start.Month() && d.Day() == min(start.Day(), last)
	}
	return false
}
func choreDue(e Entry, date string) bool {
	if e.Kind != "chore" {
		return true
	}
	e = normalizeSchedule(e)
	if e.Repeat == "" {
		return e.Date == "" || date >= e.Date
	}
	d, err := calendarDate(date)
	return err == nil && matchesDate(e, d)
}
func eventsOn(entries []Entry, date string, loc *time.Location) []Entry {
	d, err := calendarDate(date)
	if err != nil {
		return nil
	}
	var result []Entry
	for _, e := range entries {
		if matchesDate(e, d) {
			e.Date = date
			if Validate(e, loc) == nil {
				result = append(result, e)
			}
		}
	}
	return result
}
func repeatLabel(e Entry) string {
	e = normalizeSchedule(e)
	if e.Repeat == "" {
		return "One time"
	}
	if e.Repeat == "weekdays" {
		return "Weekdays"
	}
	units := map[string]string{"daily": "day", "weekly": "week", "custom": "week", "monthly": "month", "yearly": "year"}
	label := map[string]string{"daily": "Daily", "weekly": "Weekly", "custom": "Selected days", "monthly": "Monthly", "yearly": "Yearly"}[e.Repeat]
	if e.RepeatEvery > 1 {
		label = fmt.Sprintf("Every %d %ss", e.RepeatEvery, units[e.Repeat])
	}
	if e.Repeat == "custom" {
		names := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
		var days []string
		for _, i := range []int{1, 2, 3, 4, 5, 6, 0} {
			if e.RepeatDays&(1<<i) != 0 {
				days = append(days, names[i])
			}
		}
		label = strings.Join(days, ", ")
		if e.RepeatEvery > 1 {
			label += fmt.Sprintf(" · every %d weeks", e.RepeatEvery)
		}
	}
	return label
}
func parseSchedule(value string, interval string, days []string) (string, int, int, error) {
	every := 1
	var err error
	if interval != "" {
		every, err = strconv.Atoi(interval)
		if err != nil {
			return value, 1, 0, errors.New("Enter a whole number for the repeat interval.")
		}
	}
	if every < 1 || every > 99 {
		return value, every, 0, errors.New("Repeat every 1–99 days, weeks, months or years.")
	}
	mask := 0
	for _, day := range days {
		n, e := strconv.Atoi(day)
		if e != nil || n < 0 || n > 6 {
			return value, every, mask, errors.New("Choose valid days of the week.")
		}
		mask |= 1 << n
	}
	return value, every, mask, nil
}
