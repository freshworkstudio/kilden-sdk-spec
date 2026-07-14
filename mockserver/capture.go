package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"
)

const (
	maxBatchEvents = 1000
	maxEventName   = 200
	maxDistinctID  = 512
	maxBodyBytes   = 5 << 20
)

// SPEC.md is stricter than production where uniformity matters: canonical
// UUIDs, exact millisecond-UTC timestamps, no unknown payload keys. An SDK
// that passes here passes production, not vice versa.
var (
	uuidRe      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	timestampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
)

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		preflight(w)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.applyFailure(w) {
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	reader := io.Reader(body)
	isGzip := r.Header.Get("Content-Encoding") == "gzip"
	if isGzip {
		gz, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "invalid gzip body", http.StatusBadRequest)
			return
		}
		defer gz.Close()
		reader = gz
	}

	raw, err := io.ReadAll(reader)
	if err != nil {
		// MaxBytesReader tripping means the request was too large.
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	var batch struct {
		WriteKey *string           `json:"write_key"`
		SentAt   *string           `json:"sent_at"`
		Batch    []json.RawMessage `json:"batch"`
	}
	if err := strictUnmarshal(raw, &batch, "write_key", "sent_at", "batch", "identity_token"); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if batch.WriteKey == nil || *batch.WriteKey == "" {
		http.Error(w, "write_key is required", http.StatusBadRequest)
		return
	}
	if len(batch.Batch) == 0 {
		http.Error(w, "batch must not be empty", http.StatusBadRequest)
		return
	}
	if len(batch.Batch) > maxBatchEvents {
		http.Error(w, fmt.Sprintf("batch exceeds %d events", maxBatchEvents), http.StatusBadRequest)
		return
	}
	if batch.SentAt == nil || !timestampRe.MatchString(*batch.SentAt) {
		http.Error(w, "sent_at is required and must be YYYY-MM-DDTHH:MM:SS.mmmZ", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	knownKey := s.publicKeys[*batch.WriteKey] || s.secretKeys[*batch.WriteKey]
	origins := s.origins
	s.mu.Unlock()
	if !knownKey {
		http.Error(w, "unknown write_key", http.StatusUnauthorized)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && len(origins) > 0 && !originAllowed(origins, origin) {
		http.Error(w, "origin not allowed for this project", http.StatusForbidden)
		return
	}

	events := make([]capturedEvent, 0, len(batch.Batch))
	for i, rawEvent := range batch.Batch {
		var e capturedEvent
		if err := strictUnmarshal(rawEvent, &e, "uuid", "event", "distinct_id", "properties", "timestamp"); err != nil {
			http.Error(w, fmt.Sprintf("batch[%d]: %s", i, err), http.StatusBadRequest)
			return
		}
		if err := validateEvent(e); err != nil {
			http.Error(w, fmt.Sprintf("batch[%d]: %s", i, err), http.StatusBadRequest)
			return
		}
		events = append(events, e)
	}

	headers := map[string]string{}
	for _, h := range []string{"Content-Type", "Content-Encoding", "User-Agent"} {
		if v := r.Header.Get(h); v != "" {
			headers[h] = v
		}
	}
	s.mu.Lock()
	s.batches = append(s.batches, capturedBatch{
		WriteKey: *batch.WriteKey,
		SentAt:   *batch.SentAt,
		Batch:    events,
		Gzip:     isGzip,
		Headers:  headers,
	})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func validateEvent(e capturedEvent) error {
	if !uuidRe.MatchString(e.UUID) {
		return fmt.Errorf("uuid is not a canonical UUID")
	}
	if e.Event == "" {
		return fmt.Errorf("event is required")
	}
	if len(e.Event) > maxEventName {
		return fmt.Errorf("event exceeds %d chars", maxEventName)
	}
	if e.DistinctID == "" {
		return fmt.Errorf("distinct_id is required")
	}
	if len(e.DistinctID) > maxDistinctID {
		return fmt.Errorf("distinct_id exceeds %d chars", maxDistinctID)
	}
	if len(e.Properties) == 0 {
		return fmt.Errorf("properties is required (send {} when empty)")
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal(e.Properties, &props); err != nil {
		return fmt.Errorf("properties must be a JSON object")
	}
	if !timestampRe.MatchString(e.Timestamp) {
		return fmt.Errorf("timestamp must be YYYY-MM-DDTHH:MM:SS.mmmZ")
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", e.Timestamp); err != nil {
		return fmt.Errorf("timestamp is not a real instant: %s", e.Timestamp)
	}
	return nil
}

// strictUnmarshal decodes JSON into v and rejects keys outside allowed —
// the spec freezes the exact payload shape, so an SDK inventing keys is a
// bug the mock must catch.
func strictUnmarshal(data []byte, v any, allowed ...string) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	for k := range keys {
		if !slices.Contains(allowed, k) {
			return fmt.Errorf("unknown key %q", k)
		}
	}
	return json.Unmarshal(data, v)
}

func originAllowed(allowed []string, origin string) bool {
	return slices.Contains(allowed, origin)
}

func preflight(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Content-Encoding")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

// applyFailure consumes one armed failure and acts it out. Returns true when
// the request was hijacked by the failure.
func (s *Server) applyFailure(w http.ResponseWriter) bool {
	f, ok := s.nextFailure()
	if !ok {
		return false
	}
	switch f.Mode {
	case "timeout":
		delay := f.DelayMs
		if delay == 0 {
			delay = 10000
		}
		time.Sleep(time.Duration(delay) * time.Millisecond)
		w.Write([]byte(`{"status":"ok"}`))
	case "corrupt":
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": <<< not json`))
	case "cut":
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return true
		}
		buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n{\"stat")
		buf.Flush()
		conn.Close()
	default: // "status"
		if f.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(f.RetryAfter))
		}
		http.Error(w, http.StatusText(f.Status), f.Status)
	}
	return true
}
