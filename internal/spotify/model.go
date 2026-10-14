package spotify

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const CallbackPath = "/media/spotify/callback"
const Scopes = "user-read-playback-state user-read-currently-playing user-modify-playback-state user-read-private"

var clientPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

type Config struct{ ClientID, Redirect, Revision string }

func (c Config) Validate() error {
	if !clientPattern.MatchString(c.ClientID) {
		return errors.New("Enter your 32-character Spotify Client ID")
	}
	u, e := url.Parse(c.Redirect)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != CallbackPath {
		return errors.New("Use the registered Spotify callback address")
	}
	if u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return errors.New("Use an HTTP loopback callback (127.0.0.1 or [::1]), with a port")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return errors.New("Include the loopback callback port")
	}
	return nil
}
func (c Config) Configured() bool { return c.Validate() == nil }
func (c Config) Origin() string {
	u, e := url.Parse(c.Redirect)
	if e != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
func (c Config) MatchesHost(host string) bool {
	u, e := url.Parse(c.Redirect)
	return e == nil && u.Host == host
}
func Random() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

type Pending struct {
	StateHash, BrowserHash, Verifier, Redirect, ClientID, ConfigRevision string
	Expires                                                              time.Time
}
type Credential struct {
	AccessToken, RefreshToken, ClientID, Scope, AccountID, AccountName, Revision string
	Expires                                                                      time.Time
}

func (c Credential) Connected() bool { return c.RefreshToken != "" && c.ClientID != "" }
func (c Credential) CanControl() bool {
	for _, s := range strings.Fields(c.Scope) {
		if s == "user-modify-playback-state" {
			return true
		}
	}
	return false
}

type Device struct {
	ID, Name, Type                     string
	Active, Restricted, SupportsVolume bool
	Volume                             *int
}

func (d Device) Valid() bool {
	return d.ID != "" && len(d.ID) <= 180 && !strings.ContainsAny(d.ID, "\r\n\x00") && len(d.Name) <= 320 && (d.Volume == nil || *d.Volume >= 0 && *d.Volume <= 100)
}

type Snapshot struct {
	Devices                                                      []Device
	Device                                                       Device
	Title, Artists, Album, ItemURL, ImageURL, ImageKey, ItemType string
	Playing                                                      bool
	Progress, Duration                                           int64
	Fetched                                                      time.Time
	Disallows                                                    map[string]bool
}

func (s Snapshot) Fresh(now time.Time) bool {
	return !s.Fetched.IsZero() && now.Sub(s.Fetched) < 45*time.Second
}
func (s Snapshot) Percent() int {
	if s.Duration <= 0 {
		return 0
	}
	return int(min(100, max(0, s.Progress*100/s.Duration)))
}
func (s Snapshot) Elapsed() string { return clock(s.Progress) }
func (s Snapshot) Length() string  { return clock(s.Duration) }
func clock(ms int64) string {
	seconds := max(0, ms/1000)
	return strconv.FormatInt(seconds/60, 10) + ":" + fmtSeconds(seconds%60)
}
func fmtSeconds(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}
func (s Snapshot) Validate() error {
	if len(s.Devices) > 64 || len(s.Title) > 640 || len(s.Artists) > 640 || len(s.Album) > 640 || s.Progress < 0 || s.Duration < 0 || s.Progress > 604800000 || s.Duration > 604800000 {
		return errors.New("Invalid Spotify playback data")
	}
	for _, d := range s.Devices {
		if !d.Valid() {
			return errors.New("Invalid Spotify device")
		}
	}
	if s.Device.ID != "" && !s.Device.Valid() {
		return errors.New("Invalid Spotify player")
	}
	if s.ImageURL != "" && !AllowedImage(s.ImageURL) {
		return errors.New("Invalid Spotify artwork")
	}
	if s.ItemURL != "" {
		u, e := url.Parse(s.ItemURL)
		if e != nil || u.Scheme != "https" || u.Host != "open.spotify.com" || u.User != nil {
			return errors.New("Invalid Spotify link")
		}
	}
	return nil
}
func Text(s string, n int) string {
	v := []rune(strings.Join(strings.Fields(s), " "))
	if len(v) > n {
		v = v[:n]
	}
	return string(v)
}
func AllowedImage(address string) bool {
	u, e := url.Parse(address)
	return e == nil && u.Scheme == "https" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Host == "i.scdn.co" || u.Host == "mosaic.scdn.co")
}

type View struct {
	AccountID                                                                  string `json:"-"`
	Configured, Connected, Fresh, ControlScope, Artwork, DevicesFresh, Pending bool
	Account, Error, Updated                                                    string
	State                                                                      Snapshot
}

func (v View) Controls() bool {
	return v.Connected && v.ControlScope && v.Fresh && v.State.Device.Valid() && !v.State.Device.Restricted
}
func (v View) VolumeControls() bool {
	return v.Controls() && v.State.Device.SupportsVolume && v.State.Device.Volume != nil && !v.State.Disallows["setting_volume"]
}
func (v View) PlayAllowed() bool     { return v.Controls() && !v.State.Disallows["resuming"] }
func (v View) PauseAllowed() bool    { return v.Controls() && !v.State.Disallows["pausing"] }
func (v View) NextAllowed() bool     { return v.Controls() && !v.State.Disallows["skipping_next"] }
func (v View) PreviousAllowed() bool { return v.Controls() && !v.State.Disallows["skipping_prev"] }
func (v View) TransferAllowed() bool {
	return v.Connected && v.Fresh && v.ControlScope && v.DevicesFresh && !v.State.Disallows["transferring_playback"]
}

func (v View) SelectionAllowed() bool {
	if !v.Connected || !v.Fresh || !v.ControlScope || !v.DevicesFresh || v.State.Disallows["interrupting_playback"] {
		return false
	}
	for _, d := range v.State.Devices {
		if d.Valid() && !d.Restricted {
			return true
		}
	}
	return false
}
