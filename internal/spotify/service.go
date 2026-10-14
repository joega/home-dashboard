package spotify

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrChanged = errors.New("Spotify connection changed; reload and try again")

type Repository interface {
	SpotifyConfig() (Config, error)
	SpotifyCredential() (Credential, error)
	SwapSpotifyCredential(string, Credential) error
}
type Provider interface {
	Refresh(context.Context, Credential) (Credential, error)
	Playback(context.Context, Credential) (Snapshot, error)
	Devices(context.Context, Credential) ([]Device, error)
	Command(context.Context, Credential, Device, string) error
	Artwork(context.Context, string) ([]byte, string, error)
}
type Service struct {
	Repo                Repository
	Provider            Provider
	work                sync.Mutex
	mu                  sync.Mutex
	state               Snapshot
	revision, message   string
	controls            bool
	devices             []Device
	devicesAt           time.Time
	imageKey, imageType string
	image               []byte
	next, lastCommand   time.Time
	wake                chan struct{}
	selections          map[string]selection
	lastSearch          time.Time
}

func NewService(r Repository, p Provider) *Service {
	return &Service{Repo: r, Provider: p, wake: make(chan struct{}, 1)}
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Reset() {
	s.mu.Lock()
	s.state = Snapshot{}
	s.revision = ""
	s.message = ""
	s.next = time.Time{}
	s.devices = nil
	s.devicesAt = time.Time{}
	s.image = nil
	s.imageKey = ""
	s.selections = nil
	s.mu.Unlock()
	s.Wake()
}
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		s.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.wake:
		}
	}
}
func (s *Service) credential(ctx context.Context) (Credential, error) {
	c, e := s.Repo.SpotifyCredential()
	if e != nil {
		return c, e
	}
	if !c.Connected() {
		return c, errors.New("Connect Spotify in Media settings")
	}
	cfg, e := s.Repo.SpotifyConfig()
	if e != nil || c.ClientID != cfg.ClientID {
		return c, ErrChanged
	}
	if time.Until(c.Expires) < time.Minute {
		updated, e := s.Provider.Refresh(ctx, c)
		if e != nil {
			return c, e
		}
		if e = s.Repo.SwapSpotifyCredential(c.Revision, updated); e != nil {
			return c, e
		}
		c = updated
	}
	return c, nil
}
func (s *Service) fail(e error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message = e.Error()
	s.next = time.Now().Add(time.Minute)
	var rate RateError
	if errors.As(e, &rate) {
		s.next = time.Now().Add(rate.Delay)
	}
}
func (s *Service) Refresh(ctx context.Context) {
	s.work.Lock()
	defer s.work.Unlock()
	s.mu.Lock()
	wait := time.Now().Before(s.next)
	s.mu.Unlock()
	if wait {
		return
	}
	raw, e := s.Repo.SpotifyCredential()
	if e != nil || !raw.Connected() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	credential, e := s.credential(ctx)
	if e != nil {
		s.fail(e)
		return
	}
	s.mu.Lock()
	if s.revision != credential.Revision {
		s.devicesAt = time.Time{}
	}
	ds := append([]Device(nil), s.devices...)
	devicesDue := time.Since(s.devicesAt) > time.Minute
	s.mu.Unlock()
	devicesErr := error(nil)
	if devicesDue {
		ds, devicesErr = s.Provider.Devices(ctx, credential)
		if devicesErr != nil {
			s.fail(devicesErr)
			return
		}
	}
	state, e := s.Provider.Playback(ctx, credential)
	if e != nil {
		s.fail(e)
		return
	}
	found := false
	for _, d := range ds {
		if d.ID == state.Device.ID {
			found = true
			break
		}
	}
	if !found && state.Device.Valid() {
		ds = append(ds, state.Device)
	}
	state.Devices = ds
	if e = state.Validate(); e != nil {
		s.fail(e)
		return
	}
	// Disconnect/reconnect or configuration changes must discard late results.
	current, e := s.Repo.SpotifyCredential()
	if e != nil || current.Revision != credential.Revision {
		return
	}
	s.mu.Lock()
	s.state = state
	s.revision = credential.Revision
	s.controls = credential.CanControl()
	s.message = ""
	s.next = time.Time{}
	s.devices = ds
	if devicesDue {
		s.devicesAt = time.Now()
	}
	imageDue := state.ImageKey != "" && state.ImageKey != s.imageKey
	if state.ImageKey == "" {
		s.image = nil
		s.imageKey = ""
	}
	s.mu.Unlock()
	if imageDue {
		body, kind, e := s.Provider.Artwork(ctx, state.ImageURL)
		if e == nil {
			current, _ := s.Repo.SpotifyCredential()
			s.mu.Lock()
			if current.Revision == credential.Revision && s.state.ImageKey == state.ImageKey {
				s.image = body
				s.imageType = kind
				s.imageKey = state.ImageKey
			}
			s.mu.Unlock()
		}
	}
}
func (s *Service) View(now time.Time) View {
	cfg, _ := s.Repo.SpotifyConfig()
	c, _ := s.Repo.SpotifyCredential()
	s.mu.Lock()
	defer s.mu.Unlock()
	v := View{AccountID: c.AccountID, Configured: cfg.Configured(), Connected: c.Connected() && c.ClientID == cfg.ClientID, Account: c.AccountName, Error: s.message}
	v.Pending = v.Connected && s.message == "" && (s.revision != c.Revision || s.state.Fetched.IsZero())
	if !v.Connected || s.revision != c.Revision {
		return v
	}
	v.State = s.state
	v.Fresh = s.state.Fresh(now) && s.message == ""
	v.ControlScope = s.controls
	v.Artwork = s.imageKey != "" && s.imageKey == s.state.ImageKey
	v.DevicesFresh = now.Sub(s.devicesAt) < 75*time.Second
	if !s.state.Fetched.IsZero() {
		v.Updated = s.state.Fetched.In(now.Location()).Format("3:04 PM")
	}
	return v
}
func (s *Service) Artwork(key string) ([]byte, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" || key != s.imageKey || key != s.state.ImageKey {
		return nil, ""
	}
	return append([]byte(nil), s.image...), s.imageType
}
func (s *Service) Command(ctx context.Context, id, action string) error {
	return s.command(ctx, id, action, nil, nil)
}
func (s *Service) SetVolume(ctx context.Context, id string, level int) error {
	if level < 0 || level > 100 || level%5 != 0 {
		return errors.New("Choose a volume from 0 to 100 in 5% steps")
	}
	return s.command(ctx, id, "volume-set", &level, nil)
}
func (s *Service) command(ctx context.Context, id, action string, level *int, chosen *selection) error {
	switch action {
	case "play", "pause", "next", "previous", "volume-up", "volume-down", "volume-set", "selection", "transfer":
	default:
		return errors.New("Unsupported Spotify command")
	}
	if !s.work.TryLock() {
		return errors.New("Spotify is checking status; try again in a moment")
	}
	defer s.work.Unlock()
	v := s.View(time.Now())
	if !v.Connected || !v.Fresh || !v.ControlScope {
		return errors.New("Controls need a fresh Spotify status; reconnect or wait for the next check")
	}
	s.mu.Lock()
	if time.Now().Before(s.next) || time.Since(s.lastCommand) < 750*time.Millisecond {
		s.mu.Unlock()
		return errors.New("Wait a moment before another Spotify command")
	}
	s.mu.Unlock()
	d := v.State.Device
	if action == "transfer" || action == "selection" {
		if !v.DevicesFresh || (action == "transfer" && v.State.Disallows["transferring_playback"]) || (action == "selection" && v.State.Disallows["interrupting_playback"]) {
			return ErrRestricted
		}
		d = Device{}
		for _, device := range v.State.Devices {
			if device.ID == id {
				d = device
				break
			}
		}
	} else if id != d.ID {
		return errors.New("This is no longer the active Spotify device")
	}
	if !d.Valid() || d.Restricted {
		return ErrRestricted
	}
	disallow := map[string]string{"play": "resuming", "pause": "pausing", "next": "skipping_next", "previous": "skipping_prev", "volume-set": "setting_volume", "volume-up": "setting_volume", "volume-down": "setting_volume"}
	if v.State.Disallows[disallow[action]] {
		return ErrRestricted
	}
	if (action == "volume-up" || action == "volume-down" || action == "volume-set") && (!d.SupportsVolume || d.Volume == nil) {
		return errors.New("Spotify does not expose volume controls for this device")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	c, e := s.credential(ctx)
	if e != nil {
		return e
	}
	if c.AccountID != v.AccountID {
		return ErrChanged
	}
	if !c.CanControl() {
		return ErrRestricted
	}
	s.mu.Lock()
	s.lastCommand = time.Now()
	s.mu.Unlock()
	switch action {
	case "volume-set":
		provider, ok := s.Provider.(VolumeSetter)
		if !ok || level == nil {
			return errors.New("Volume control is unavailable")
		}
		e = provider.SetVolume(ctx, c, d, *level)
	case "selection":
		provider, ok := s.Provider.(Catalog)
		if !ok || chosen == nil || chosen.account != c.AccountID || chosen.client != c.ClientID || time.Now().After(chosen.expires) {
			return errors.New("Search again before playing this selection")
		}
		e = provider.PlayItem(ctx, c, d, chosen.item)
	default:
		e = s.Provider.Command(ctx, c, d, action)
	}
	if e != nil {
		s.fail(e)
	} else {
		s.mu.Lock()
		s.state.Fetched = time.Time{}
		s.devicesAt = time.Time{}
		s.mu.Unlock()
		s.Wake()
	}
	return e
}
