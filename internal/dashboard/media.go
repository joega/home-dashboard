package dashboard

import (
	"context"
	"home-dashboard/internal/media"
	"net/http"
	"time"
)

func (a *App) RunMedia(ctx context.Context) { a.media.Run(ctx) }
func (a *App) registerMedia(mux *http.ServeMux) {
	mux.HandleFunc("POST /media/discover", a.discoverMedia)
	mux.HandleFunc("POST /media/add", a.addMedia)
	mux.HandleFunc("GET /media/{id}/edit", a.mediaForm)
	mux.HandleFunc("POST /media/{id}/settings", a.saveMedia)
	mux.HandleFunc("POST /media/{id}/pair", a.pairMedia)
	mux.HandleFunc("POST /media/{id}/forget", a.forgetMedia)
	mux.HandleFunc("POST /media/{id}/command", a.commandMedia)
}
func (a *App) mediaReply(w http.ResponseWriter, r *http.Request, notice string, err error) {
	p := a.base(w, r)
	p.Return = "/media"
	if r.Form.Get("return") == "/settings?section=media" {
		p.Return = r.Form.Get("return")
	}
	if err != nil {
		p.Error = err.Error()
		if r.URL.Path == "/media/add" {
			p.MediaDraft = media.Device{Name: r.Form.Get("name"), Host: r.Form.Get("host"), Kind: r.Form.Get("kind")}
		}
	} else {
		p.Notice = notice
	}
	a.changed(w, r, p)
}
func (a *App) discoverMedia(w http.ResponseWriter, r *http.Request) {
	a.media.RequestScan()
	a.mediaReply(w, r, "Looking for TVs and Cast devices…", nil)
}
func (a *App) addMedia(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	d, e := media.NewClient().Probe(ctx, r.Form.Get("host"), r.Form.Get("kind"), r.Form.Get("name"))
	if e == nil {
		e = a.store.AddProbedMediaDevice(d)
	}
	a.media.Wake()
	a.mediaReply(w, r, "Device added. Checking its status…", e)
}
func (a *App) mediaForm(w http.ResponseWriter, r *http.Request) {
	p := a.base(w, r)
	ds, e := a.store.MediaDevices()
	if e != nil {
		a.fail(w, e)
		return
	}
	for _, d := range ds {
		if d.ID == r.PathValue("id") {
			p.MediaDevice = d
			p.Media = a.media.View(p.Now)
			for _, c := range p.Media.Cards {
				if c.Device.ID == d.ID {
					p.MediaCard = c
				}
			}
			p.Return = "/media"
			if r.URL.Query().Get("return") == "settings" {
				p.Return = "/settings?section=media"
			}
			a.render(w, "media-form", p)
			return
		}
	}
	http.NotFound(w, r)
}
func (a *App) saveMedia(w http.ResponseWriter, r *http.Request) {
	e := a.store.UpdateMediaDevice(r.PathValue("id"), r.Form.Get("name"), r.Form.Has("enabled"))
	a.media.Wake()
	a.mediaReply(w, r, "Device settings saved.", e)
}
func (a *App) pairMedia(w http.ResponseWriter, r *http.Request) {
	d, e := a.media.Device(r.PathValue("id"))
	if e == nil {
		if d.Kind == "samsung" {
			e = a.media.PairSamsung(d.ID)
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			e = a.media.PairSony(ctx, d.ID, r.Form.Get("key"))
		}
	}
	a.mediaReply(w, r, "Pairing requested. Samsung: approve Home HQ on your TV; Sony: the key was accepted.", e)
}
func (a *App) forgetMedia(w http.ResponseWriter, r *http.Request) {
	e := a.media.Forget(r.PathValue("id"))
	a.mediaReply(w, r, "Pairing and saved device keys forgotten. Revoke Home HQ on the TV too if desired.", e)
}
func (a *App) commandMedia(w http.ResponseWriter, r *http.Request) {
	e := a.media.Command(r.Context(), r.PathValue("id"), r.Form.Get("command"))
	a.mediaReply(w, r, "Command sent. Waiting for updated device status…", e)
}
