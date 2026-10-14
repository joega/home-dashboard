package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/netip"
	"strings"
	"time"
)

const MaxDevices = 16

type Device struct {
	ID, Host, Name, Model, Kind, UID string
	Aliases                          []string
	Cast, Enabled, CustomName        bool
	Seen                             time.Time
}

func ID(uid string) string { h := sha256.Sum256([]byte(uid)); return hex.EncodeToString(h[:12]) }
func PrivateHost(host string) bool {
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Is4() && ip.IsPrivate() && !ip.IsLoopback()
}
func (d Device) Validate() error {
	if !PrivateHost(d.Host) || d.ID != ID(d.UID) || d.UID == "" || len(d.UID) > 180 || d.Name == "" || len(d.Name) > 400 || len(d.Model) > 400 {
		return errors.New("Invalid media device")
	}
	if len(d.Aliases) > 4 {
		return errors.New("Too many device identities")
	}
	for _, id := range d.Aliases {
		if id == "" || len(id) > 180 {
			return errors.New("Invalid device identity")
		}
	}
	if d.Kind != "sony" && d.Kind != "samsung" && d.Kind != "cast" {
		return errors.New("Unsupported media device")
	}
	return nil
}
func Text(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	v := []rune(s)
	if len(v) > n {
		v = v[:n]
	}
	return string(v)
}
func (d Device) Brand() string {
	switch d.Kind {
	case "sony":
		return "Sony BRAVIA"
	case "samsung":
		return "Samsung TV"
	default:
		return "Google Cast"
	}
}

type Credential struct {
	Host, SonyPSK, SamsungToken, SamsungPin, CastPin string
}

func (c Credential) Paired(d Device) bool {
	return d.Kind == "sony" && c.SonyPSK != "" && c.Host == d.Host || d.Kind == "samsung" && c.SamsungToken != "" && c.SamsungPin != ""
}

type Snapshot struct {
	Reachable, Native, AuthRequired, ArtKnown, ArtMode bool
	Power, App, Title, Artist, Album, Playback, Note   string
	Volume                                             *int
	Muted                                              *bool
	Position, Duration                                 float64
	Updated                                            time.Time
	// Pins never enter HTML, JSON status or logs.
	CastPin string `json:"-"`
}

func (s Snapshot) PowerLabel() string {
	switch s.Power {
	case "standby":
		return "Standby"
	case "on":
		if s.ArtKnown && s.ArtMode {
			return "Art Mode"
		}
		return "On"
	case "off":
		return "Off"
	}
	if s.Reachable {
		return "Network available"
	}
	return "Unavailable"
}
func (s Snapshot) Playing() bool {
	return s.Reachable && s.Power != "standby" && s.Power != "off" && s.Title != "" && s.Playback != "IDLE"
}
func (s Snapshot) PlaybackLabel() string {
	switch s.Playback {
	case "PLAYING":
		return "Playing"
	case "PAUSED":
		return "Paused"
	case "BUFFERING":
		return "Buffering"
	case "IDLE":
		return "Idle"
	}
	return "Playback details unavailable"
}
func (s Snapshot) Validate() error {
	for _, s := range []string{s.App, s.Title, s.Artist, s.Album, s.Note} {
		if len(s) > 640 {
			return errors.New("Oversized media status")
		}
	}
	if s.Volume != nil && (*s.Volume < 0 || *s.Volume > 100) {
		return errors.New("Invalid volume")
	}
	if math.IsNaN(s.Position) || math.IsNaN(s.Duration) || math.IsInf(s.Position, 0) || math.IsInf(s.Duration, 0) || s.Position < 0 || s.Duration < 0 || s.Position > 604800 || s.Duration > 604800 {
		return errors.New("Invalid media progress")
	}
	switch s.Power {
	case "", "on", "standby", "off":
	default:
		return errors.New("Invalid media power state")
	}
	return nil
}

// Multiple protocols identify the same physical TV. Match only advertised
// identities, never a reused DHCP address, when moving a discovered record.
func SameIdentity(a, b Device) bool {
	if a.UID == b.UID {
		return true
	}
	for _, v := range a.Aliases {
		if v == b.UID {
			return true
		}
		for _, w := range b.Aliases {
			if v == w {
				return true
			}
		}
	}
	for _, v := range b.Aliases {
		if v == a.UID {
			return true
		}
	}
	return false
}
func IdentityAliases(ds ...Device) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, d := range ds {
		for _, v := range append([]string{d.UID}, d.Aliases...) {
			if v != "" && !seen[v] && len(out) < 4 {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}
