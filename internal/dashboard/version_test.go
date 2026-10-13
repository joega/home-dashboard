package dashboard

import (
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestApplicationVersionTracksAllUIFilesAndBuild(t *testing.T) {
	files := fstest.MapFS{
		"static/app.js":      &fstest.MapFile{Data: []byte("script")},
		"templates/app.html": &fstest.MapFile{Data: []byte("page")},
	}
	first, err := applicationVersion(files, "commit-a")
	if err != nil || len(first) != 24 {
		t.Fatal(first, err)
	}
	again, _ := applicationVersion(files, "commit-a")
	if again != first {
		t.Fatal("same build has unstable version")
	}
	other, _ := applicationVersion(files, "commit-b")
	if other == first {
		t.Fatal("backend revision did not change version")
	}
	files["templates/app.html"].Data = []byte("new markup")
	other, _ = applicationVersion(files, "commit-a")
	if other == first {
		t.Fatal("template change did not change version")
	}
	files["static/fonts/new.woff2"] = &fstest.MapFile{Data: []byte("font")}
	fontVersion, _ := applicationVersion(files, "commit-a")
	if fontVersion == other {
		t.Fatal("new asset did not change version")
	}
	if _, err = applicationVersion(brokenVersionFS{}, "commit-a"); err == nil {
		t.Fatal("ignored embedded read error")
	}
}

type brokenVersionFS struct{}

func (brokenVersionFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }

func TestVersionEndpointMatchesLoadedDocumentAndDoesNotFetchWeather(t *testing.T) {
	s, _ := testStore(t)
	provider := &dashboardWeatherProvider{}
	loc, _ := time.LoadLocation("America/New_York")
	a, err := New(s, loc, provider)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/version", nil))
	var response struct {
		Version string `json:"version"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || response.Version != a.version || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" || provider.fetches != 0 {
		t.Fatal("invalid version response", w.Code, w.Header(), response, provider.fetches)
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://household.test/", nil))
	if !strings.Contains(w.Body.String(), `data-app-version="`+response.Version+`"`) || !strings.Contains(w.Body.String(), `/static/update.js?v=`+response.Version) {
		t.Fatal("document did not pin the loaded application version")
	}
}
