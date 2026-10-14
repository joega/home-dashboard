package sports

import (
	"context"
	"log"
	"sync"
	"time"
)

type Repository interface {
	SportsSettings() (Settings, error)
	LoadSportsCache(string) (*Snapshot, error)
	SaveSportsCache(Snapshot) error
	LoadSportsLogo(string) ([]byte, error)
	SaveSportsLogo(string, []byte) error
}
type Provider interface {
	Fetch(context.Context, Team, time.Time) (Snapshot, error)
	FetchLogo(context.Context, Team) ([]byte, error)
}
type slot struct {
	snapshot                   *Snapshot
	loaded, failed, logoLoaded bool
	failures                   int
	next, logoNext             time.Time
}
type Service struct {
	repo     Repository
	Provider Provider
	loc      *time.Location
	mu       sync.RWMutex
	refresh  sync.Mutex
	slots    map[string]slot
	wake     chan struct{}
}

func NewService(repo Repository, p Provider, loc *time.Location) *Service {
	return &Service{repo: repo, Provider: p, loc: loc, slots: map[string]slot{}, wake: make(chan struct{}, 1)}
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		s.Refresh(ctx, time.Now().In(s.loc))
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-tick.C:
		}
	}
}
func (s *Service) States(c Settings, now time.Time) []TeamState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	states := []TeamState{}
	for _, t := range c.Selected() {
		entry := s.slots[t.Key]
		state := TeamState{Team: t, Ready: entry.loaded}
		if entry.snapshot == nil {
			state.Unavailable = entry.loaded && entry.failed
		} else {
			state.Fetched = entry.snapshot.Fetched
			state.Stale = entry.failed || now.After(entry.next)
			state.Unavailable = now.Sub(entry.snapshot.Fetched) > 7*24*time.Hour || entry.snapshot.Fetched.After(now.Add(5*time.Minute))
			if !state.Unavailable {
				state.Games = append([]Game(nil), entry.snapshot.Games...)
			}
		}
		states = append(states, state)
	}
	return states
}
func (s *Service) Refresh(ctx context.Context, now time.Time) {
	s.refresh.Lock()
	defer s.refresh.Unlock()
	cfg, err := s.repo.SportsSettings()
	if err != nil {
		log.Print("sports settings: ", err)
		return
	}
	for _, t := range cfg.Selected() {
		if ctx.Err() != nil {
			return
		}
		s.mu.RLock()
		entry := s.slots[t.Key]
		s.mu.RUnlock()
		if !entry.loaded {
			cached, e := s.repo.LoadSportsCache(t.Key)
			if e != nil {
				log.Print("sports cache: ", e)
			}
			if cached != nil && cached.Validate(t) == nil && !cached.Fetched.After(now.Add(5*time.Minute)) {
				entry.snapshot = cached
				entry.next = nextRefresh(*cached, cached.Fetched)
			}
			entry.loaded = true
		}
		if !now.Before(entry.next) {
			fresh, e := s.Provider.Fetch(ctx, t, now)
			if e == nil {
				e = fresh.Validate(t)
			}
			if e == nil && (fresh.Fetched.After(now.Add(time.Minute)) || fresh.Fetched.Before(now.Add(-time.Minute))) {
				e = context.DeadlineExceeded
			}
			if e == nil && entry.snapshot != nil {
				hasFinal := false
				for _, g := range fresh.Games {
					if g.State == "final" {
						hasFinal = true
						break
					}
				}
				if !hasFinal && len(fresh.Games) < 256 {
					var latest *Game
					for i := range entry.snapshot.Games {
						g := entry.snapshot.Games[i]
						if g.State == "final" && (latest == nil || g.Start.After(latest.Start)) {
							copy := g
							latest = &copy
						}
					}
					if latest != nil {
						duplicate := false
						for _, g := range fresh.Games {
							if g.ID == latest.ID {
								duplicate = true
							}
						}
						if !duplicate {
							latest.Retained = true
							fresh.Games = append(fresh.Games, *latest)
						}
					}
				}
			}
			if e == nil {
				e = s.repo.SaveSportsCache(fresh)
			}
			if e != nil {
				entry.failed = true
				entry.failures++
				delay := time.Minute << min(entry.failures-1, 4)
				entry.next = now.Add(min(delay, 15*time.Minute))
				log.Printf("sports %s: %v", t.Key, e)
			} else {
				entry.snapshot = &fresh
				entry.failed = false
				entry.failures = 0
				entry.next = nextRefresh(fresh, now)
			}
		}
		s.mu.Lock()
		s.slots[t.Key] = entry
		s.mu.Unlock()
		// Logos never block schedule reads or make an otherwise good schedule stale.
		if !entry.logoLoaded {
			image, e := s.repo.LoadSportsLogo(t.Key)
			if e == nil && len(image) > 0 {
				entry.logoLoaded = true
			}
			if !entry.logoLoaded && !now.Before(entry.logoNext) {
				b, e := s.Provider.FetchLogo(ctx, t)
				if e == nil {
					e = s.repo.SaveSportsLogo(t.Key, b)
				}
				if e == nil {
					entry.logoLoaded = true
				} else {
					entry.logoNext = now.Add(24 * time.Hour)
				}
			}
		}
		s.mu.Lock()
		s.slots[t.Key] = entry
		s.mu.Unlock()
	}
}
