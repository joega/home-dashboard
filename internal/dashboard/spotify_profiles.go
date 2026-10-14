package dashboard

import (
	"context"
	"home-dashboard/internal/spotify"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type spotifyProfileRepo struct {
	store *Store
	id    int64
}

func (r spotifyProfileRepo) SpotifyConfig() (spotify.Config, error) { return r.store.SpotifyConfig() }
func (r spotifyProfileRepo) SpotifyCredential() (spotify.Credential, error) {
	if r.id > 0 {
		p, e := r.store.Person(r.id)
		if e != nil || !p.Active {
			return spotify.Credential{}, nil
		}
	}
	return r.store.SpotifyCredentialFor(r.id)
}
func (r spotifyProfileRepo) SwapSpotifyCredential(old string, c spotify.Credential) error {
	return r.store.SwapSpotifyCredentialFor(r.id, old, c)
}
func (a *App) spotifyService(id int64) *spotify.Service {
	if id == 0 {
		return a.spotify
	}
	a.spotifyMu.Lock()
	defer a.spotifyMu.Unlock()
	if a.spotifyProfiles == nil {
		a.spotifyProfiles = map[int64]*spotify.Service{}
	}
	if a.spotifyProfiles[id] == nil {
		a.spotifyProfiles[id] = spotify.NewService(spotifyProfileRepo{a.store, id}, a.spotify.Provider)
	}
	return a.spotifyProfiles[id]
}
func (a *App) wakeSpotifyProfiles() {
	select {
	case a.spotifyWake <- struct{}{}:
	default:
	}
}
func (a *App) resetSpotifyProfiles() {
	a.spotify.Reset()
	a.spotifyMu.Lock()
	defer a.spotifyMu.Unlock()
	for _, service := range a.spotifyProfiles {
		service.Reset()
	}
	a.wakeSpotifyProfiles()
}
func (a *App) RunSpotify(ctx context.Context) {
	var wg sync.WaitGroup
	started := map[int64]bool{}
	defer wg.Wait()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		ids, _ := a.store.SpotifyProfileIDs()
		ids = append(ids, 0)
		for _, id := range ids {
			if !started[id] {
				started[id] = true
				service := a.spotifyService(id)
				wg.Add(1)
				go func() { defer wg.Done(); service.Run(ctx) }()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-a.spotifyWake:
		}
	}
}

type SpotifyLink struct {
	Person    Person
	Connected bool
	Account   string
}

func (a *App) loadSpotifyProfiles(p *Page) error {
	for _, person := range p.People {
		credential, e := a.store.SpotifyCredentialFor(person.ID)
		if e != nil {
			return e
		}
		p.SpotifyLinks = append(p.SpotifyLinks, SpotifyLink{Person: person, Connected: credential.Connected(), Account: credential.AccountName})
	}
	ids, e := a.store.SpotifyProfileIDs()
	if e != nil {
		return e
	}
	for _, id := range ids {
		person := Person{Name: "Unassigned Spotify account", Active: true}
		if id > 0 {
			person, e = a.store.Person(id)
			if e != nil {
				return e
			}
			if !person.Active {
				continue
			}
		}
		view := a.spotifyService(id).View(p.Now)
		if view.Connected {
			p.SpotifyCards = append(p.SpotifyCards, Page{Spotify: view, SpotifyPerson: person, SpotifyPersonID: id, CSRF: p.CSRF})
			p.SpotifyPolling = true
			p.SpotifyPending = p.SpotifyPending || view.Pending
		}
	}
	sort.SliceStable(p.SpotifyCards, func(i, j int) bool {
		left, right := p.SpotifyCards[i], p.SpotifyCards[j]
		if left.SpotifyPersonID == p.CurrentPerson.ID && p.CurrentPerson.ID > 0 {
			return true
		}
		if right.SpotifyPersonID == p.CurrentPerson.ID && p.CurrentPerson.ID > 0 {
			return false
		}
		return strings.ToLower(left.SpotifyPerson.Name) < strings.ToLower(right.SpotifyPerson.Name)
	})
	// The existing unassigned connection remains visible until explicitly linked.
	p.Spotify = a.spotify.View(p.Now)
	return nil
}
func (a *App) spotifyTarget(w http.ResponseWriter, r *http.Request, allowArchived bool) (int64, bool) {
	value := r.Form.Get("spotify_person_id")
	if r.Method == "GET" {
		value = r.URL.Query().Get("spotify_person_id")
	}
	if value == "" {
		return 0, true
	}
	id, e := strconv.ParseInt(value, 10, 64)
	if e != nil || id < 0 {
		http.Error(w, "Choose a Spotify profile", 400)
		return 0, false
	}
	if id > 0 {
		p, e := a.store.Person(id)
		if e != nil || (!allowArchived && !p.Active) {
			http.Error(w, "Choose an active Spotify profile", 400)
			return 0, false
		}
	}
	return id, true
}
func (a *App) assignSpotify(w http.ResponseWriter, r *http.Request) {
	id, ok := a.spotifyTarget(w, r, false)
	if !ok {
		return
	}
	e := a.store.AssignLegacySpotify(id)
	if e == nil {
		a.spotify.Reset()
		a.spotifyService(id).Reset()
		a.wakeSpotifyProfiles()
	}
	r.Form.Set("return", "/settings?section=media")
	a.mediaReply(w, r, "Existing Spotify connection linked to this person.", e)
}
