package wled

import (
	"fmt"
	"net"
	"sync"
	"time"

	"pwled/internal/config"
)

// watchdogInterval is the maximum gap between sent packets.
// WLED times out receivers after 500 ms of silence; we use 450 ms to stay safe.
const watchdogInterval = 450 * time.Millisecond

// Sender transmits WLED Audio Sync v2 packets over UDP.
// A built-in watchdog sends decayed copies of the last packet when audio goes
// quiet so WLED effects fade to zero instead of freezing.
//
// When SetActive(false) is called the sender pauses entirely — both real
// frames and watchdog decay packets are suppressed — so other WLED-Audio-Sync
// senders on the LAN can take over after WLED's ~500 ms receive timeout.
type Sender struct {
	conn     *net.UDPConn
	cfg      *config.Store
	mu       sync.Mutex
	last     Packet
	counter  uint8
	watchdog *time.Timer
	active   bool
}

// NewSender opens a UDP socket to dest:port. The store is read each watchdog
// tick to pick up live decay-rate changes.
// Use "255.255.255.255" for LAN broadcast or a specific host for unicast.
func NewSender(dest string, port int, store *config.Store) (*Sender, error) {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", dest, port))
	if err != nil {
		return nil, fmt.Errorf("resolve dest: %w", err)
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("dial UDP: %w", err)
	}
	s := &Sender{conn: conn, cfg: store, active: true}
	s.watchdog = time.AfterFunc(watchdogInterval, s.onWatchdog)
	return s, nil
}

// SetActive toggles whether the sender emits packets. While inactive both
// Send() and the silence watchdog are suppressed, freeing the UDP stream for
// other senders. The next SetActive(true) restores normal transmission.
func (s *Sender) SetActive(active bool) {
	s.mu.Lock()
	s.active = active
	s.mu.Unlock()
}

// Send transmits p and resets the silence watchdog.
// FrameCounter is set automatically. Returns nil without sending while
// inactive.
func (s *Sender) Send(p Packet) error {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return nil
	}
	s.counter++
	p.FrameCounter = s.counter
	s.last = p
	raw := p.Marshal()
	s.mu.Unlock()

	s.watchdog.Reset(watchdogInterval)
	_, err := s.conn.Write(raw[:])
	return err
}

func (s *Sender) onWatchdog() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		// Don't reschedule — the next Send() will arm the watchdog again
		// when activity resumes.
		return
	}
	decay := float64(s.cfg.Load().Watchdog.DecayPerFrame)
	p := s.last.Decay(decay)
	s.last = p
	raw := p.Marshal()
	s.mu.Unlock()

	s.conn.Write(raw[:]) //nolint:errcheck
	s.watchdog.Reset(watchdogInterval)
}

// Close stops the watchdog and closes the UDP socket.
func (s *Sender) Close() error {
	s.watchdog.Stop()
	return s.conn.Close()
}
