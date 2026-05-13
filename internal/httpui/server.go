package httpui

import (
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"pwled/internal/config"
)

//go:embed index.html
var indexHTML []byte

// Frame is the JSON payload sent to each WebSocket client per audio frame.
type Frame struct {
	Raw       float32   `json:"raw"`
	Smooth    float32   `json:"smooth"`
	Peak      bool      `json:"peak"`
	Bands     [16]uint8 `json:"bands"`
	Magnitude float32   `json:"magnitude"`
	MajorPeak float32   `json:"majorPeak"`
	Active    bool      `json:"active"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

// Server is an HTTP server that streams audio frames to browser clients and
// exposes the live DSP config via a small JSON API.
type Server struct {
	cfg     *config.Store
	mu      sync.RWMutex
	clients map[chan []byte]struct{}
}

// New creates a Server backed by the given config store.
func New(cfg *config.Store) *Server {
	return &Server{
		cfg:     cfg,
		clients: make(map[chan []byte]struct{}),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/config/reset", s.handleConfigReset)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.ServeHTTP(w, r)
}

// Listen starts the HTTP server on addr and returns.
func (s *Server) Listen(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	slog.Info("http", "addr", "http://"+ln.Addr().String())
	go http.Serve(ln, s)
	return ln.Addr().String(), nil
}

// Publish broadcasts a frame to all connected WebSocket clients.
func (s *Server) Publish(f Frame) {
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.clients {
		select {
		case ch <- b:
		default: // slow client — drop frame
		}
	}
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg.Load())
	case http.MethodPut:
		var next config.Config
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&next); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
			return
		}
		if err := s.cfg.Apply(next); err != nil {
			var ve *config.ValidationError
			if errors.As(err, &ve) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":  "validation failed",
					"fields": ve.Fields,
				})
				return
			}
			// In-memory swap stood; persistence failed. Tell the client but
			// still return the now-active config.
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":  "applied in-memory but persistence failed: " + err.Error(),
				"config": s.cfg.Load(),
			})
			return
		}
		writeJSON(w, http.StatusOK, s.cfg.Load())
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfigReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.cfg.Apply(config.Default()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":  "reset failed: " + err.Error(),
			"config": s.cfg.Load(),
		})
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.Load())
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := make(chan []byte, 32)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()

	// discard incoming messages; detect close via read error
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				close(ch)
				return
			}
		}
	}()

	for b := range ch {
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			return
		}
	}
}
