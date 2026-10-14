package dashboard

import (
	"context"
	"home-dashboard/internal/spotify"
	"net/http"
	"time"
)

const spotifyCookie = "hq_spotify_login"

func (a *App) RunSpotify(ctx context.Context) { a.spotify.Run(ctx) }
func (a *App) registerSpotify(mux *http.ServeMux) {
	mux.HandleFunc("POST /media/spotify/settings", a.saveSpotify)
	mux.HandleFunc("POST /media/spotify/connect", a.connectSpotify)
	mux.HandleFunc("GET "+spotify.CallbackPath, a.spotifyCallback)
	mux.HandleFunc("POST /media/spotify/disconnect", a.disconnectSpotify)
	mux.HandleFunc("POST /media/spotify/command", a.spotifyCommand)
	mux.HandleFunc("GET /media/spotify/art/{key}", a.spotifyArtwork)
}
func (a *App) saveSpotify(w http.ResponseWriter, r *http.Request) {
	cfg := spotify.Config{ClientID: r.Form.Get("client_id"), Redirect: r.Form.Get("redirect")}
	e := a.store.SaveSpotifyConfig(cfg)
	if e == nil {
		a.spotify.Reset()
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
	cfg, e := a.store.SpotifyConfig()
	if e != nil || !cfg.Configured() {
		http.Error(w, "Save your Spotify Client ID and callback first", 400)
		return
	}
	if !cfg.MatchesHost(r.Host) {
		http.Error(w, "Open "+cfg.Origin()+"/settings?section=media to connect Spotify. On Mighty, use the documented temporary SSH tunnel.", 400)
		return
	}
	state, browser, verifier := spotify.Random(), spotify.Random(), spotify.Random()
	p := spotify.Pending{StateHash: spotify.Hash(state), BrowserHash: spotify.Hash(browser), Verifier: verifier, Redirect: cfg.Redirect, ClientID: cfg.ClientID, ConfigRevision: cfg.Revision, Expires: time.Now().Add(10 * time.Minute)}
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
		e = a.store.SaveSpotifyConnected(pending.ConfigRevision, credential)
	}
	if e != nil {
		a.spotifyResult(w, r, e.Error(), true)
		return
	}
	a.spotify.Reset()
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
	e := a.store.DisconnectSpotify()
	if e == nil {
		a.spotify.Reset()
	}
	a.mediaReply(w, r, "Spotify disconnected. Playback has not been changed.", e)
}
func (a *App) spotifyCommand(w http.ResponseWriter, r *http.Request) {
	e := a.spotify.Command(r.Context(), r.Form.Get("device_id"), r.Form.Get("action"))
	a.mediaReply(w, r, "Spotify command sent. Checking playback…", e)
}
func (a *App) spotifyArtwork(w http.ResponseWriter, r *http.Request) {
	if !a.spotify.View(time.Now()).Connected {
		http.NotFound(w, r)
		return
	}
	b, kind := a.spotify.Artwork(r.PathValue("key"))
	if len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(b)
}
