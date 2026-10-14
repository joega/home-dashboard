package dashboard

import (
	"context"
	"home-dashboard/internal/media"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type dashboardMediaProvider struct{ calls, commands int }

func (p *dashboardMediaProvider) Read(_ context.Context, d media.Device, _ media.Credential) (media.Snapshot, error) {
	p.calls++
	return media.Snapshot{Reachable: true, Power: "on", Title: "<script>movie</script>", Playback: "PLAYING", Updated: time.Now()}, nil
}
func (*dashboardMediaProvider) PairSamsung(context.Context, media.Device, media.Credential) (media.Credential, error) {
	return media.Credential{}, media.ErrAuth
}
func (*dashboardMediaProvider) PairSony(context.Context, media.Device, media.Credential, string) (media.Credential, error) {
	return media.Credential{}, media.ErrAuth
}
func (p *dashboardMediaProvider) Command(context.Context, media.Device, media.Credential, string) error {
	p.commands++
	return nil
}
func TestMediaPersistenceDHCPAndCredentialIsolation(t *testing.T) {
	s, _ := testStore(t)
	d := media.Device{UID: "cast:stable", Host: "192.168.50.38", Name: "Den TV", Kind: "sony", Enabled: true, Cast: true}
	d.ID = media.ID(d.UID)
	if e := s.MergeMediaDevices([]media.Device{d}); e != nil {
		t.Fatal(e)
	}
	if e := s.UpdateMediaDevice(d.ID, "Family TV", false); e != nil {
		t.Fatal(e)
	}
	secret := media.Credential{Host: d.Host, SonyPSK: "never-render-this-key"}
	if e := s.SaveMediaCredential(d.ID, secret); e != nil {
		t.Fatal(e)
	}
	if e := s.SaveMediaPin(d.ID, strings.Repeat("a", 64)); e != nil {
		t.Fatal(e)
	}
	saved, _ := s.MediaCredential(d.ID)
	if saved.SonyPSK != secret.SonyPSK || saved.CastPin == "" {
		t.Fatal("pin overwrote key", saved)
	}
	if e := s.SaveMediaCredential(d.ID, secret); e != nil {
		t.Fatal(e)
	}
	saved, _ = s.MediaCredential(d.ID)
	if saved.CastPin == "" {
		t.Fatal("pair completion erased saved pin")
	}
	d.Aliases = []string{d.UID}
	d.UID = "airplay:same-tv"
	d.ID = media.ID(d.UID)
	d.Host = "192.168.50.41"
	d.Name = "Advertised name"
	if e := s.MergeMediaDevices([]media.Device{d}); e != nil {
		t.Fatal(e)
	}
	ds, _ := s.MediaDevices()
	if len(ds) != 1 || ds[0].Host != d.Host || ds[0].Name != "Family TV" || ds[0].Enabled || saved.Paired(ds[0]) {
		t.Fatal("DHCP update discarded preference or carried PSK to new host", ds)
	}
	d.ID = ds[0].ID
	s.UpdateMediaDevice(d.ID, "Family TV", true)
	p := &dashboardMediaProvider{}
	a, e := New(s, time.UTC)
	if e != nil {
		t.Fatal(e)
	}
	a.media = media.NewService(s, p, nil)
	a.media.Refresh(context.Background())
	before := p.calls
	for _, path := range []string{"/media", "/settings?section=media", "/media/" + d.ID + "/edit"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://home.test"+path, nil))
		body := w.Body.String()
		if w.Code != 200 || strings.Contains(body, secret.SonyPSK) || strings.Contains(body, saved.CastPin) || strings.Contains(body, "<script>movie</script>") {
			t.Fatal(path, w.Code, body)
		}
		if path == "/media" && !strings.Contains(body, "&lt;script&gt;movie&lt;/script&gt;") {
			t.Fatal("missing escaped title")
		}
	}
	if p.calls != before {
		t.Fatal("GET page queried device")
	}
	// Commands need both CSRF and pairing at the current address.
	values := url.Values{"csrf": {strings.Repeat("a", 64)}, "command": {"pause"}}
	for _, cookie := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "http://home.test/media/"+d.ID+"/command", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		if cookie {
			r.AddCookie(&http.Cookie{Name: "hq_csrf", Value: strings.Repeat("a", 64)})
		}
		a.ServeHTTP(w, r)
		if !cookie && w.Code != 403 {
			t.Fatal("missing CSRF accepted")
		}
		if p.commands != 0 {
			t.Fatal("unpaired command sent")
		}
	}
	secret.Host = d.Host
	s.SaveMediaCredential(d.ID, secret)
	values.Set("command", "power")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "http://home.test/media/"+d.ID+"/command", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r.AddCookie(&http.Cookie{Name: "hq_csrf", Value: strings.Repeat("a", 64)})
	a.ServeHTTP(w, r)
	if p.commands != 0 || !strings.Contains(w.Body.String(), "Unsupported media command") {
		t.Fatal("arbitrary command accepted")
	}
}
func TestMediaManualAddressGuards(t *testing.T) {
	s, _ := testStore(t)
	for _, host := range []string{"8.8.8.8", "127.0.0.1", "169.254.169.254", "http://192.168.50.1", "192.168.50.1:22"} {
		if _, e := s.AddMediaDevice(host, "samsung", "TV"); e == nil {
			t.Fatal(host)
		}
	}
	d, e := s.AddMediaDevice("192.168.50.36", "samsung", "Kitchen TV")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AddMediaDevice(d.Host, "samsung", "Duplicate"); e == nil {
		t.Fatal("duplicate address accepted")
	}
	if s.SaveMediaCredential(d.ID, media.Credential{SamsungPin: "bad"}) == nil {
		t.Fatal("invalid certificate pin")
	}
}

func TestMediaDatabaseBackupPrivatePermissions(t *testing.T) {
	s, _ := testStore(t)
	path := filepath.Join(t.TempDir(), "private-backup.sqlite")
	if e := s.Backup(path); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}
	if s.Backup(path) == nil {
		t.Fatal("overwrote backup")
	}
}
