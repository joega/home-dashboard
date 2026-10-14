package media

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

type Repository interface {
	MediaDevices() ([]Device, error)
	MergeMediaDevices([]Device) error
	MediaCredential(string) (Credential, error)
	SaveMediaCredential(string, Credential) error
	SaveMediaPin(string, string) error
}
type Provider interface {
	Read(context.Context, Device, Credential) (Snapshot, error)
	PairSamsung(context.Context, Device, Credential) (Credential, error)
	PairSony(context.Context, Device, Credential, string) (Credential, error)
	Command(context.Context, Device, Credential, string) error
}
type Card struct {
	Device          Device
	Status          Snapshot
	Paired, Pairing bool
	Error, Updated  string
}

func (c Card) Controls() bool {
	return c.Paired && c.Status.Reachable && c.Status.Power != "standby" && c.Status.Power != "off" && !c.Status.ArtMode
}

type View struct {
	Cards              []Card
	Scanning           bool
	ScanError, Scanned string
}
type Service struct {
	Repo          Repository
	Provider      Provider
	Discovery     Discoverer
	mu            sync.Mutex
	work          sync.Mutex
	states        map[string]Snapshot
	errors        map[string]string
	pairings      map[string]bool
	pairingErrors map[string]string
	operations    sync.Mutex
	commands      map[string]time.Time
	wake          chan struct{}
	scan          bool
	scanning      bool
	scanned       time.Time
	scanError     string
	ctx           context.Context
	pairs         sync.WaitGroup
}

func NewService(repo Repository, p Provider, d Discoverer) *Service {
	return &Service{Repo: repo, Provider: p, Discovery: d, states: map[string]Snapshot{}, errors: map[string]string{}, pairings: map[string]bool{}, pairingErrors: map[string]string{}, commands: map[string]time.Time{}, wake: make(chan struct{}, 1), scan: true, ctx: context.Background()}
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) RequestScan() { s.mu.Lock(); s.scan = true; s.mu.Unlock(); s.Wake() }
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	defer s.pairs.Wait()
	for {
		s.mu.Lock()
		scan := s.scan || time.Since(s.scanned) > 5*time.Minute
		s.scan = false
		s.mu.Unlock()
		if scan {
			s.Scan(ctx)
		}
		s.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-tick.C:
		}
	}
}
func (s *Service) Scan(ctx context.Context) {
	s.mu.Lock()
	s.scanning = true
	s.mu.Unlock()
	devices, e := s.Discovery.Discover(ctx)
	if e == nil {
		e = s.Repo.MergeMediaDevices(devices)
	}
	s.mu.Lock()
	s.scanning = false
	s.scanned = time.Now()
	s.scanError = ""
	if e != nil {
		s.scanError = e.Error()
	}
	s.mu.Unlock()
}
func (s *Service) Refresh(ctx context.Context) {
	s.work.Lock()
	defer s.work.Unlock()
	devices, e := s.Repo.MediaDevices()
	if e != nil {
		return
	}
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for _, d := range devices {
		if !d.Enabled || ctx.Err() != nil {
			continue
		}
		select {
		case limit <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(d Device) {
			defer wg.Done()
			defer func() { <-limit }()
			credential, e := s.Repo.MediaCredential(d.ID)
			if e != nil {
				return
			}
			child, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			status, e := s.Provider.Read(child, d, credential)
			if e == nil {
				e = status.Validate()
			}
			s.mu.Lock()
			if e != nil {
				s.errors[d.ID] = "Not responding; it may be off or unavailable."
				old := s.states[d.ID]
				old.Reachable = false
				old.Updated = time.Now()
				old.Title = ""
				old.App = ""
				old.Artist = ""
				old.Album = ""
				old.Playback = ""
				old.Power = ""
				s.states[d.ID] = old
			} else {
				s.states[d.ID] = status
				s.errors[d.ID] = ""
			}
			s.mu.Unlock()
			if e == nil && status.CastPin != "" && credential.CastPin == "" {
				s.Repo.SaveMediaPin(d.ID, status.CastPin)
			}
		}(d)
	}
	wg.Wait()
}
func (s *Service) View(now time.Time) View {
	devices, _ := s.Repo.MediaDevices()
	s.mu.Lock()
	defer s.mu.Unlock()
	v := View{Scanning: s.scanning || s.scan, ScanError: s.scanError}
	if !s.scanned.IsZero() {
		v.Scanned = s.scanned.In(now.Location()).Format("3:04 PM")
	}
	for _, d := range devices {
		if !d.Enabled {
			continue
		}
		credential, _ := s.Repo.MediaCredential(d.ID)
		status := s.states[d.ID]
		message := s.errors[d.ID]
		if s.pairingErrors[d.ID] != "" {
			message = s.pairingErrors[d.ID]
		}
		if !status.Updated.IsZero() && now.Sub(status.Updated) > 90*time.Second {
			status.Reachable = false
			status.Power = ""
			status.Title = ""
			status.App = ""
			status.Artist = ""
			status.Album = ""
			status.Playback = ""
			message = "Status is stale; waiting for the device."
		}
		card := Card{Device: d, Status: status, Paired: credential.Paired(d), Pairing: s.pairings[d.ID], Error: message}
		if !status.Updated.IsZero() {
			card.Updated = status.Updated.In(now.Location()).Format("3:04 PM")
		}
		v.Cards = append(v.Cards, card)
	}
	sort.Slice(v.Cards, func(i, j int) bool {
		a, b := v.Cards[i].Device, v.Cards[j].Device
		if (a.Kind == "cast") != (b.Kind == "cast") {
			return a.Kind != "cast"
		}
		return a.Name < b.Name
	})
	return v
}
func (s *Service) Device(id string) (Device, error) {
	ds, e := s.Repo.MediaDevices()
	if e != nil {
		return Device{}, e
	}
	for _, d := range ds {
		if d.ID == id && d.Enabled {
			return d, nil
		}
	}
	return Device{}, errors.New("Media device no longer available")
}
func (s *Service) PairSamsung(id string) error {
	d, e := s.Device(id)
	if e != nil || d.Kind != "samsung" {
		return errors.New("Choose a Samsung TV")
	}
	old, e := s.Repo.MediaCredential(id)
	if e != nil {
		return e
	}
	s.mu.Lock()
	if s.pairings[id] {
		s.mu.Unlock()
		return nil
	}
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return errors.New("Server stopping")
	}
	s.pairings[id] = true
	s.pairingErrors[id] = ""
	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	s.pairs.Add(1)
	s.mu.Unlock()
	go func() {
		s.operations.Lock()
		defer s.operations.Unlock()
		defer s.pairs.Done()
		defer cancel()
		credential, e := s.Provider.PairSamsung(ctx, d, old)
		if e == nil {
			e = s.Repo.SaveMediaCredential(id, credential)
		}
		s.mu.Lock()
		s.pairings[id] = false
		if e != nil {
			s.pairingErrors[id] = "Pairing was declined or timed out. Retry and approve Home HQ on the TV."
		}
		s.mu.Unlock()
		s.Wake()
	}()
	return nil
}
func (s *Service) PairSony(ctx context.Context, id, key string) error {
	if !s.operations.TryLock() {
		return errors.New("Another TV pairing is in progress. Try again shortly.")
	}
	defer s.operations.Unlock()
	d, e := s.Device(id)
	if e != nil {
		return e
	}
	old, e := s.Repo.MediaCredential(id)
	if e != nil {
		return e
	}
	credential, e := s.Provider.PairSony(ctx, d, old, key)
	if e != nil {
		return e
	}
	if e = s.Repo.SaveMediaCredential(id, credential); e == nil {
		s.Wake()
	}
	return e
}
func (s *Service) Command(ctx context.Context, id, command string) error {
	if !ValidCommand(command) {
		return errors.New("Unsupported media command")
	}
	d, e := s.Device(id)
	if e != nil {
		return e
	}
	credential, e := s.Repo.MediaCredential(id)
	if e != nil || !credential.Paired(d) {
		return errors.New("Pair this TV first")
	}
	s.mu.Lock()
	status := s.states[id]
	if !status.Reachable || status.Power == "standby" || status.Power == "off" || status.ArtMode || time.Since(status.Updated) > 90*time.Second {
		s.mu.Unlock()
		return errors.New("Controls are available when the TV is on and its status is fresh")
	}
	if time.Since(s.commands[id]) < 500*time.Millisecond {
		s.mu.Unlock()
		return errors.New("Wait a moment before the next command")
	}
	s.commands[id] = time.Now()
	s.mu.Unlock()
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	e = s.Provider.Command(child, d, credential, command)
	if e == nil {
		s.Wake()
	}
	return e
}

// Forget refuses while a TV approval is pending; it cannot resurrect a removed token.
func (s *Service) Forget(id string) error {
	s.mu.Lock()
	busy := s.pairings[id]
	s.mu.Unlock()
	if busy {
		return errors.New("Wait for pairing to finish before forgetting this device")
	}
	if !s.operations.TryLock() {
		return errors.New("Another TV pairing is in progress. Try again shortly.")
	}
	defer s.operations.Unlock()
	s.mu.Lock()
	busy = s.pairings[id]
	s.mu.Unlock()
	if busy {
		return errors.New("Pairing is in progress")
	}
	if e := s.Repo.SaveMediaCredential(id, Credential{}); e != nil {
		return e
	}
	s.mu.Lock()
	delete(s.pairingErrors, id)
	delete(s.states, id)
	s.mu.Unlock()
	s.Wake()
	return nil
}
