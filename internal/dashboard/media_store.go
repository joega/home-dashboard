package dashboard

import (
	"database/sql"
	"encoding/json"
	"errors"
	"home-dashboard/internal/media"
	"strings"
	"time"
)

func (s *Store) migrateMedia() error {
	_, e := s.db.Exec(`CREATE TABLE IF NOT EXISTS media_devices(id TEXT PRIMARY KEY,body BLOB NOT NULL); CREATE TABLE IF NOT EXISTS media_credentials(id TEXT PRIMARY KEY REFERENCES media_devices(id) ON DELETE CASCADE,body BLOB NOT NULL)`)
	return e
}
func (s *Store) MediaDevices() ([]media.Device, error) {
	rows, e := s.db.Query("SELECT body FROM media_devices ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ds := []media.Device{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var d media.Device
		if len(b) > 4096 || json.Unmarshal(b, &d) != nil || d.Validate() != nil {
			return nil, errors.New("Invalid saved media device")
		}
		ds = append(ds, d)
	}
	return ds, rows.Err()
}
func (s *Store) MergeMediaDevices(in []media.Device) error {
	if len(in) > media.MaxDevices {
		return errors.New("Too many media devices")
	}
	for _, d := range in {
		if e := d.Validate(); e != nil {
			return e
		}
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	rows, e := tx.Query("SELECT body FROM media_devices")
	if e != nil {
		return e
	}
	ds := []media.Device{}
	for rows.Next() {
		var b []byte
		var d media.Device
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return e
		}
		if json.Unmarshal(b, &d) != nil {
			rows.Close()
			return errors.New("Invalid media data")
		}
		ds = append(ds, d)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, found := range in {
		match := -1
		for i, old := range ds {
			if media.SameIdentity(old, found) {
				match = i
				break
			}
		}
		// Coalesce protocols advertising the same TV, while keeping the first stable
		// identity. Discovery updates a DHCP address only for an exact identity match.
		if match < 0 {
			for i, old := range ds {
				if old.Host == found.Host && old.Kind == found.Kind && strings.HasPrefix(old.UID, "manual:") {
					match = i
					break
				}
			}
		}
		if match >= 0 {
			old := ds[match]
			found.Aliases = media.IdentityAliases(found, old)
			found.ID = old.ID
			found.UID = old.UID
			found.Enabled = old.Enabled
			found.CustomName = old.CustomName
			if old.CustomName {
				found.Name = old.Name
			}
			found.Cast = old.Cast || found.Cast
			ds[match] = found
		} else if len(ds) < media.MaxDevices {
			ds = append(ds, found)
		}
	}
	for _, d := range ds {
		b, _ := json.Marshal(d)
		if _, e = tx.Exec("INSERT INTO media_devices(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", d.ID, b); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) MediaCredential(id string) (media.Credential, error) {
	var b []byte
	e := s.db.QueryRow("SELECT body FROM media_credentials WHERE id=?", id).Scan(&b)
	if e == sql.ErrNoRows {
		return media.Credential{}, nil
	}
	if e != nil {
		return media.Credential{}, e
	}
	var c media.Credential
	if len(b) > 4096 || json.Unmarshal(b, &c) != nil {
		return c, errors.New("Invalid media credential")
	}
	return c, nil
}
func validMediaCredential(c media.Credential) bool {
	return len(c.SonyPSK) <= 128 && len(c.SamsungToken) <= 256 && (c.Host == "" || media.PrivateHost(c.Host)) && validMediaPin(c.CastPin) && validMediaPin(c.SamsungPin)
}
func validMediaPin(p string) bool {
	if p == "" {
		return true
	}
	if len(p) != 64 {
		return false
	}
	for _, v := range p {
		if !(v >= '0' && v <= '9' || v >= 'a' && v <= 'f') {
			return false
		}
	}
	return true
}
func (s *Store) SaveMediaCredential(id string, c media.Credential) error {
	if !validMediaCredential(c) {
		return errors.New("Invalid pairing")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// A first Cast read can save its public key while TV approval is in flight.
	// Preserve that pin when adding authentication; an explicit empty credential
	// (Forget) clears all fields instead.
	if (c.SonyPSK != "" || c.SamsungToken != "") && c.CastPin == "" {
		var b []byte
		e = tx.QueryRow("SELECT body FROM media_credentials WHERE id=?", id).Scan(&b)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		var current media.Credential
		if len(b) > 0 && json.Unmarshal(b, &current) != nil {
			return errors.New("Invalid pairing")
		}
		c.CastPin = current.CastPin
	}
	b, _ := json.Marshal(c)
	_, e = tx.Exec("INSERT INTO media_credentials(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", id, b)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SaveMediaPin(id, pin string) error {
	if pin == "" || !validMediaPin(pin) {
		return errors.New("Invalid device key")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var b []byte
	var c media.Credential
	e = tx.QueryRow("SELECT body FROM media_credentials WHERE id=?", id).Scan(&b)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	if len(b) > 0 && json.Unmarshal(b, &c) != nil {
		return errors.New("Invalid pairing")
	}
	if c.CastPin != "" {
		return tx.Commit()
	}
	c.CastPin = pin
	b, _ = json.Marshal(c)
	_, e = tx.Exec("INSERT INTO media_credentials(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", id, b)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) UpdateMediaDevice(id, name string, enabled bool) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var b []byte
	if e = tx.QueryRow("SELECT body FROM media_devices WHERE id=?", id).Scan(&b); e != nil {
		return e
	}
	var d media.Device
	if json.Unmarshal(b, &d) != nil {
		return errors.New("Invalid device")
	}
	d.Name = media.Text(name, 60)
	d.CustomName = true
	d.Enabled = enabled
	if e = d.Validate(); e != nil {
		return e
	}
	b, _ = json.Marshal(d)
	_, e = tx.Exec("UPDATE media_devices SET body=? WHERE id=?", b, id)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) AddMediaDevice(host, kind, name string) (media.Device, error) {
	d := media.Device{Host: host, Kind: kind, Name: media.Text(name, 60), UID: "manual:" + kind + ":" + host, Enabled: true, Seen: time.Now(), CustomName: true, Cast: kind == "cast"}
	d.ID = media.ID(d.UID)
	return d, s.AddProbedMediaDevice(d)
}
func (s *Store) AddProbedMediaDevice(d media.Device) error {
	if e := d.Validate(); e != nil {
		return e
	}
	ds, e := s.MediaDevices()
	if e != nil {
		return e
	}
	for _, old := range ds {
		if old.Host == d.Host {
			return errors.New("This address is already saved")
		}
	}
	if len(ds) >= media.MaxDevices {
		return errors.New("Up to 16 devices can be saved")
	}
	return s.MergeMediaDevices([]media.Device{d})
}
