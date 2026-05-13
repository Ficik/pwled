package config

import (
	"sync"
	"sync/atomic"
)

// Store is the runtime container for the active Config. It is safe for
// concurrent use: the audio loop calls Load() once per frame; HTTP handlers
// call Apply() to swap in a new config and persist it to disk.
//
// The persist callback is invoked synchronously inside Apply, so an HTTP
// handler can return an error to the client if disk write fails. The
// in-memory swap happens first either way — disk persistence is best-effort.
type Store struct {
	cur     atomic.Pointer[Config]
	persist func(Config) error

	mu   sync.Mutex
	subs []chan *Config
}

// NewStore wraps an initial config and a persistence callback.
// Pass nil for persist to disable disk writes (useful in tests).
func NewStore(initial Config, persist func(Config) error) *Store {
	s := &Store{persist: persist}
	c := initial
	s.cur.Store(&c)
	return s
}

// Load returns the current config. Callers must not mutate the returned value.
func (s *Store) Load() *Config {
	return s.cur.Load()
}

// Apply validates the candidate config, swaps it in, then persists.
// Validation failures leave the previous config untouched and return the
// validation error before any swap. A persistence failure is returned but
// the in-memory swap stands.
//
// Subscribers (see Subscribe) are notified after the in-memory swap and
// before persistence — so they always observe an up-to-date Load().
func (s *Store) Apply(next Config) error {
	if err := next.Validate(); err != nil {
		return err
	}
	c := next
	s.cur.Store(&c)
	s.notify()
	if s.persist != nil {
		if err := s.persist(c); err != nil {
			return err
		}
	}
	return nil
}

// Subscribe returns a buffered channel that receives the current Config
// whenever Apply succeeds. The current value is delivered immediately so
// callers can sync without polling. Sends are non-blocking — slow consumers
// drop intermediate updates rather than back-pressure the audio path.
func (s *Store) Subscribe() <-chan *Config {
	ch := make(chan *Config, 4)
	s.mu.Lock()
	s.subs = append(s.subs, ch)
	s.mu.Unlock()
	ch <- s.cur.Load()
	return ch
}

func (s *Store) notify() {
	s.mu.Lock()
	subs := append([]chan *Config(nil), s.subs...)
	s.mu.Unlock()
	cur := s.cur.Load()
	for _, ch := range subs {
		select {
		case ch <- cur:
		default: // slow subscriber — drop this update
		}
	}
}
