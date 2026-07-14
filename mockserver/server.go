package main

import (
	"encoding/json"
	"net/http"
	"sync"
)

// Server holds all mock state behind one mutex: captured batches, configured
// flags, armed failures and the key/origin configuration. Contention is
// irrelevant here — fidelity and inspectability are the point.
type Server struct {
	mu sync.Mutex

	publicKeys map[string]bool
	secretKeys map[string]bool
	origins    []string

	batches  []capturedBatch
	flags    []Flag
	failures []failure

	mux *http.ServeMux
}

type capturedBatch struct {
	WriteKey string            `json:"write_key"`
	SentAt   string            `json:"sent_at"`
	Batch    []capturedEvent   `json:"batch"`
	Gzip     bool              `json:"gzip"`
	Headers  map[string]string `json:"headers"`
}

type capturedEvent struct {
	UUID       string          `json:"uuid"`
	Event      string          `json:"event"`
	DistinctID string          `json:"distinct_id"`
	Properties json.RawMessage `json:"properties"`
	Timestamp  string          `json:"timestamp"`
}

type failure struct {
	Mode       string `json:"mode"` // "" or "status" | "timeout" | "corrupt" | "cut"
	Status     int    `json:"status"`
	RetryAfter int    `json:"retry_after"`
	DelayMs    int    `json:"delay_ms"`
}

// Flag mirrors the platform's decide config shape (SPEC.md §8, docs/18).
type Flag struct {
	Key               string    `json:"key"`
	Active            bool      `json:"active"`
	RolloutPercentage int       `json:"rollout_percentage"`
	Variants          []Variant `json:"variants"`
}

type Variant struct {
	Key               string `json:"key"`
	RolloutPercentage int    `json:"rollout_percentage"`
}

func NewServer() *Server {
	s := &Server{}
	s.resetLocked()

	mux := http.NewServeMux()
	mux.HandleFunc("/capture", s.handleCapture)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/__mock/captured", s.handleCaptured)
	mux.HandleFunc("/__mock/reset", s.handleReset)
	mux.HandleFunc("/__mock/flags", s.handleFlags)
	mux.HandleFunc("/__mock/fail", s.handleFail)
	mux.HandleFunc("/__mock/keys", s.handleKeys)
	mux.HandleFunc("/__mock/origins", s.handleOrigins)
	s.mux = mux
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) resetLocked() {
	s.publicKeys = map[string]bool{"wk_test_public": true}
	s.secretKeys = map[string]bool{"sk_test_secret": true}
	s.origins = nil
	s.batches = nil
	s.flags = nil
	s.failures = nil
}

// nextFailure pops the oldest armed failure, if any.
func (s *Server) nextFailure() (failure, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.failures) == 0 {
		return failure{}, false
	}
	f := s.failures[0]
	s.failures = s.failures[1:]
	return f, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// --- control endpoints ---

func (s *Server) handleCaptured(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events := []capturedEvent{}
	batches := s.batches
	if batches == nil {
		batches = []capturedBatch{}
	}
	for _, b := range batches {
		events = append(events, b.Batch...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"batches": batches, "events": events})
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	s.resetLocked()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFlags(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Flags []Flag `json:"flags"`
	}
	if !decodeControl(w, r, &body) {
		return
	}
	s.mu.Lock()
	s.flags = body.Flags
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleFail(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Times int `json:"times"`
		failure
	}
	if !decodeControl(w, r, &body) {
		return
	}
	if body.Times <= 0 {
		body.Times = 1
	}
	if body.Mode == "" {
		body.Mode = "status"
	}
	if body.Mode == "status" && body.Status == 0 {
		http.Error(w, "status is required for mode=status", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	for i := 0; i < body.Times; i++ {
		s.failures = append(s.failures, body.failure)
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Public []string `json:"public"`
		Secret []string `json:"secret"`
	}
	if !decodeControl(w, r, &body) {
		return
	}
	s.mu.Lock()
	s.publicKeys = map[string]bool{}
	s.secretKeys = map[string]bool{}
	for _, k := range body.Public {
		s.publicKeys[k] = true
	}
	for _, k := range body.Secret {
		s.secretKeys[k] = true
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleOrigins(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Origins []string `json:"origins"`
	}
	if !decodeControl(w, r, &body) {
		return
	}
	s.mu.Lock()
	s.origins = body.Origins
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func decodeControl(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}
