package sports

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
)

//go:embed catalog.json
var catalogJSON []byte

// The bundled catalog is searchable offline. It is refreshed from the primary
// team feeds by scripts/refresh-sports-catalog.py, reviewed and shipped with the app.
var teams = loadCatalog()
var teamByKey = func() map[string]Team {
	m := make(map[string]Team, len(teams))
	for _, t := range teams {
		m[t.Key] = t
	}
	return m
}()

func loadCatalog() []Team {
	var catalog struct {
		Updated string
		Teams   []Team
	}
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic(err)
	}
	keyPattern := regexp.MustCompile(`^[a-z0-9-]{1,48}$`)
	idPattern := regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	abbrPattern := regexp.MustCompile(`^[A-Z0-9-]{1,12}$`)
	logoPattern := regexp.MustCompile(`^/i/teamlogos/[a-z]+/500/[a-z0-9_-]+\.png$`)
	routes := map[string]string{"MLB": "", "NHL": "", "NFL": "football/nfl", "NBA": "basketball/nba", "MLS": "soccer/usa.1", "EPL": "soccer/eng.1"}
	seen, identities := map[string]bool{}, map[string]bool{}
	for _, t := range catalog.Teams {
		u, err := url.Parse(t.Logo)
		route, ok := routes[t.League]
		identity := t.League + ":" + t.ProviderID
		if !ok || t.Route != route || !keyPattern.MatchString(t.Key) || !idPattern.MatchString(t.ProviderID) || !abbrPattern.MatchString(t.Abbr) || t.Name == "" || len(t.Name) > 100 || t.FullName == "" || len(t.FullName) > 120 || seen[t.Key] || identities[identity] || err != nil || u.Scheme != "https" || u.Host != "a.espncdn.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !logoPattern.MatchString(u.Path) {
			panic(fmt.Sprintf("Invalid bundled sports team %q", t.Key))
		}
		seen[t.Key], identities[identity] = true, true
	}
	if len(catalog.Teams) == 0 {
		panic("Empty sports catalog")
	}
	return catalog.Teams
}

func Teams() []Team                { return append([]Team(nil), teams...) }
func Find(key string) (Team, bool) { t, ok := teamByKey[key]; return t, ok }

// Selected retains the household's chosen order. The first six are shortcuts;
// every additional team stays accessible in the overflow picker and All teams.
func (c Settings) Selected() []Team {
	out := make([]Team, 0, len(c.Teams))
	for _, key := range c.Teams {
		if t, ok := Find(key); ok {
			out = append(out, t)
		}
	}
	return out
}
