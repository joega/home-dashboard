package weather

import (
	"context"
	"log"
	"sync"
	"time"
)

type Repository interface {
	WeatherSettings() (Settings, error)
	LoadWeatherCache(Location) (*Forecast, error)
	SaveWeatherCache(Forecast) error
}
type Service struct {
	repo            Repository
	Provider        Provider
	mu              sync.RWMutex
	forecast        *Forecast
	key             string
	failed, loading bool
	next            time.Time
	failures        int
	wake            chan struct{}
}

func NewService(repo Repository, provider Provider) *Service {
	return &Service{repo: repo, Provider: provider, wake: make(chan struct{}, 1)}
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) snapshot(cfg Settings) (*Forecast, bool, bool) {
	s.mu.RLock()
	f, failed, loading, key := s.forecast, s.failed, s.loading, s.key
	s.mu.RUnlock()
	if f == nil || key != cfg.Location.Key() {
		s.Wake()
	}
	if key != cfg.Location.Key() {
		failed = false
		loading = cfg.Enabled
	}
	return f, loading, failed
}
func (s *Service) View(cfg Settings, now time.Time) View {
	f, loading, failed := s.snapshot(cfg)
	return MakeView(cfg, f, now, loading, failed)
}
func (s *Service) ForecastView(cfg Settings, now time.Time, date string) ForecastView {
	f, loading, failed := s.snapshot(cfg)
	return MakeForecastView(cfg, f, now, date, loading, failed)
}

// Run owns all forecast requests. Browser refreshes only read the snapshot.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		s.Refresh(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}
func (s *Service) Refresh(ctx context.Context, now time.Time) {
	cfg, err := s.repo.WeatherSettings()
	if err != nil {
		log.Print("weather settings: ", err)
		return
	}
	if !cfg.Enabled {
		s.mu.Lock()
		s.loading = false
		s.mu.Unlock()
		return
	}
	key := cfg.Location.Key()
	s.mu.RLock()
	changed := s.key != key
	next := s.next
	s.mu.RUnlock()
	if changed {
		f, err := s.repo.LoadWeatherCache(cfg.Location)
		if err != nil {
			log.Print("weather cache: ", err)
		}
		if f != nil && (f.Validate(now) != nil || f.Location.Key() != key) {
			f = nil
		}
		s.mu.Lock()
		s.key = key
		s.forecast = f
		s.failed = false
		s.failures = 0
		s.next = time.Time{}
		if f != nil && now.Sub(f.RetrievedAt) < Refresh && now.Sub(f.RetrievedAt) >= 0 && now.Sub(f.Current.Time) <= 45*time.Minute {
			s.next = f.RetrievedAt.Add(Refresh)
		}
		next = s.next
		s.mu.Unlock()
	}
	if now.Before(next) {
		return
	}
	s.mu.Lock()
	s.loading = true
	s.mu.Unlock()
	requestCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	f, err := s.Provider.Fetch(requestCtx, cfg.Location, now)
	cancel()
	if ctx.Err() != nil {
		s.mu.Lock()
		s.loading = false
		s.mu.Unlock()
		return
	}
	if err == nil {
		if f.Location.Key() != key {
			err = context.Canceled
		} else {
			err = f.Validate(now)
		}
	}
	if err == nil {
		err = s.repo.SaveWeatherCache(f)
	}
	s.mu.Lock()
	s.loading = false
	if err != nil {
		s.failed = true
		s.failures++
		delay := time.Minute * time.Duration(1<<min(s.failures-1, 4))
		if delay > Refresh {
			delay = Refresh
		}
		s.next = now.Add(delay)
	} else {
		s.forecast = &f
		s.failed = false
		s.failures = 0
		s.next = now.Add(Refresh)
	}
	s.mu.Unlock()
	if err != nil {
		log.Print("weather refresh: ", err)
	}
}
