package dashboard

import (
	"context"
	"errors"
	"home-dashboard/internal/spotify"
	"net/http"
	"strconv"
	"time"
)

const spotifyCookie = "hq_spotify_login"

func (a *App) registerSpotify(mux *http.ServeMux) {
	mux.HandleFunc("POST /media/spotify/assign", a.assignSpotify)
	mux.HandleFunc("POST /media/spotify/settings", a.saveSpotify)
	mux.HandleFunc("POST /media/spotify/connect", a.connectSpotify)
	mux.HandleFunc("GET "+spotify.CallbackPath, a.spotifyCallback)
	mux.HandleFunc("POST /media/spotify/disconnect", a.disconnectSpotify)
	mux.HandleFunc("POST /media/spotify/command", a.spotifyCommand)
	mux.HandleFunc("GET /media/spotify/art/{key}", a.spotifyArtwork)
	mux.HandleFunc("POST /media/spotify/volume", a.spotifyVolume)
	mux.HandleFunc("GET /media/spotify/search", a.spotifySearchForm)
	mux.HandleFunc("POST /media/spotify/search", a.spotifySearch)
	mux.HandleFunc("POST /media/spotify/selection", a.spotifySelection)
}
func (a *App) saveSpotify(w http.ResponseWriter, r *http.Request) {
	cfg := spotify.Config{ClientID: r.Form.Get("client_id"), Redirect: r.Form.Get("redirect")}
	e := a.store.SaveSpotifyConfig(cfg)
	if e == nil {
		a.resetSpotifyProfiles()
	}
	p := a.base(w, r)
	p.Return = "/settings?section=media"
	p.Notice = "Spotify settings saved. Open the configured loopback address to connect."
	if e != nil {
		p.Error = e.Error()
		p.SpotifyConfig = cfg
		p.SpotifyDraft = true
	}
	a.changed(w, r, p)
}
func (a *App) connectSpotify(w http.ResponseWriter, r *http.Request) {
	id, ok := a.spotifyTarget(w, r, false)
	if !ok {
		return
	}
	cfg, e := a.store.SpotifyConfig()
	if e != nil || !cfg.Configured() {
		http.Error(w, "Save your Spotify Client ID and callback first", 400)
		return
	}
	if !cfg.MatchesHost(r.Host) {
		http.Error(w, "Open "+cfg.Origin()+"/settings?section=media to connect Spotify. For a remote dashboard server, use the documented temporary SSH tunnel.", 400)
		return
	}
	state, browser, verifier := spotify.Random(), spotify.Random(), spotify.Random()
	linkRevision, e := a.store.SpotifyLinkRevision(id)
	if e != nil {
		a.fail(w, e)
		return
	}
	p := spotify.Pending{PersonID: id, LinkRevision: linkRevision, StateHash: spotify.Hash(state), BrowserHash: spotify.Hash(browser), Verifier: verifier, Redirect: cfg.Redirect, ClientID: cfg.ClientID, ConfigRevision: cfg.Revision, Expires: time.Now().Add(10 * time.Minute)}
	if e = a.store.SaveSpotifyPending(p); e != nil {
		a.fail(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: spotifyCookie, Value: browser, Path: spotify.CallbackPath, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, a.spotifyClient.AuthorizationURL(cfg, p, state), http.StatusSeeOther)
}
func (a *App) spotifyCallback(w http.ResponseWriter, r *http.Request) {
	cookie, e := r.Cookie(spotifyCookie)
	if e != nil {
		a.spotifyResult(w, r, "Spotify login could not be verified. Start Connect Spotify again in the same browser.", true)
		return
	}
	pending, e := a.store.ConsumeSpotifyPending(r.URL.Query().Get("state"), cookie.Value, r.Host)
	if e != nil {
		a.spotifyResult(w, r, e.Error(), true)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: spotifyCookie, Path: spotify.CallbackPath, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	if r.URL.Query().Get("error") != "" {
		a.spotifyResult(w, r, "Spotify connection was not approved. Your existing connection is unchanged.", true)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	credential, e := a.spotifyClient.Exchange(ctx, pending, r.URL.Query().Get("code"))
	if e == nil {
		e = a.store.SaveSpotifyConnectedFor(pending.PersonID, pending.ConfigRevision, pending.LinkRevision, credential)
	}
	if e != nil {
		a.spotifyResult(w, r, e.Error(), true)
		return
	}
	a.spotifyService(pending.PersonID).Reset()
	a.wakeSpotifyProfiles()
	http.Redirect(w, r, "/media", http.StatusSeeOther)
}
func (a *App) spotifyResult(w http.ResponseWriter, r *http.Request, message string, failure bool) {
	p := a.base(w, r)
	p.Title = "Spotify"
	p.Notice = message
	p.Error = ""
	if failure {
		p.Error = message
		p.Notice = ""
	}
	a.render(w, "spotify-result", p)
}
func (a *App) disconnectSpotify(w http.ResponseWriter, r *http.Request) {
	id, ok := a.spotifyTarget(w, r, true)
	if !ok {
		return
	}
	e := a.store.DisconnectSpotifyFor(id)
	if e == nil {
		a.spotifyService(id).Reset()
	}
	a.mediaReply(w, r, "Spotify disconnected. Playback has not been changed.", e)
}
func (a *App) spotifyCommand(w http.ResponseWriter, r *http.Request) {
	id, ok := a.spotifyTarget(w, r, false)
	if !ok {
		return
	}
	service := a.spotifyService(id)
	e := service.Command(r.Context(), r.Form.Get("device_id"), r.Form.Get("action"))
	a.mediaReply(w, r, "Spotify command sent. Checking playback…", e)
}
func (a *App) spotifyArtwork(w http.ResponseWriter, r *http.Request) {
	id, ok := a.spotifyTarget(w, r, false)
	if !ok {
		return
	}
	service := a.spotifyService(id)
	if !service.View(time.Now()).Connected {
		http.NotFound(w, r)
		return
	}
	b, kind := service.Artwork(r.PathValue("key"))
	if len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(b)
}

func (a *App) spotifyVolume(w http.ResponseWriter, r *http.Request) {
	id, ok := a.spotifyTarget(w, r, false)
	if !ok {
		return
	}
	service := a.spotifyService(id)
	level, e := strconv.Atoi(r.Form.Get("volume"))
	if e != nil {
		e = errors.New("Choose a volume from 0 to 100 in 5% steps")
	} else {
		e = service.SetVolume(r.Context(), r.Form.Get("device_id"), level)
	}
	a.mediaReply(w, r, "Volume sent. Checking playback…", e)
}
func (a *App) spotifySearchForm(w http.ResponseWriter, r *http.Request) {
	id, ok := a.spotifyTarget(w, r, false)
	if !ok {
		return
	}
	service := a.spotifyService(id)
	p := a.base(w, r)
	p.Spotify = service.View(time.Now())
	p.SpotifyPersonID = id
	p.SpotifySearch.Kind = "track"
	p.SpotifySearchDevice = p.Spotify.State.Device.ID
	a.render(w, "spotify-search", p)
}
func (a *App) spotifySearch(w http.ResponseWriter, r *http.Request) {
	id, ok := a.spotifyTarget(w, r, false)
	if !ok {
		return
	}
	service := a.spotifyService(id)
	p := a.base(w, r)
	p.Spotify = service.View(time.Now())
	offset, e := strconv.Atoi(r.Form.Get("offset"))
	if e != nil {
		offset = 0
	}
	p.SpotifyPersonID = id
	p.SpotifySearch, e = service.Search(r.Context(), r.Form.Get("q"), r.Form.Get("kind"), offset)
	if e != nil {
		p.Error = e.Error()
	}
	p.Spotify = service.View(time.Now())
	a.render(w, "spotify-search-results", p)
}
func (a *App) spotifySelection(w http.ResponseWriter, r *http.Request) {
	id, ok := a.spotifyTarget(w, r, false)
	if !ok {
		return
	}
	service := a.spotifyService(id)
	e := service.PlaySelection(r.Context(), r.Form.Get("device_id"), r.Form.Get("selection"))
	if e != nil {
		p := a.base(w, r)
		p.Error = e.Error()
		w.Header().Set("HX-Retarget", "#spotify-search-notice")
		a.render(w, "spotify-search-notice", p)
		return
	}
	a.mediaReply(w, r, "Selection sent. Checking playback…", nil)
}
