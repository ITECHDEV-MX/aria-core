package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// HeartbeatPayload is the body POSTed by the Claude MCP wrapper to /watch/heartbeat.
type HeartbeatPayload struct {
	SessionID string   `json:"session_id"`
	Goal      string   `json:"goal,omitempty"`
	Project   string   `json:"project,omitempty"`
	Files     []string `json:"files,omitempty"`
}

// SessionState is what the Watcher tracks per active session.
type SessionState struct {
	SessionID    string
	Goal         string
	Project      string
	Files        map[string]struct{}
	FirstSeen    time.Time
	LastSeen     time.Time
	SummaryFired bool
}

// SummaryEvent is emitted when the Watcher decides a session has timed out
// without an explicit aria_session_summary call.
type SummaryEvent struct {
	SessionID string
	Goal      string
	Project   string
	Files     []string
	Reason    string
	Duration  time.Duration
}

// Watcher tracks heartbeats from Claude Code/Desktop sessions and fires
// SummaryEvents when a session goes silent past the configured timeout.
type Watcher struct {
	mu       sync.Mutex
	sessions map[string]*SessionState
	timeout  time.Duration
	now      func() time.Time
	onTimeout func(SummaryEvent)
}

// NewWatcher creates a Watcher with the given inactivity timeout. Pass
// onTimeout=nil to opt out of side-effects (tests use this to inspect events
// directly via Tick).
func NewWatcher(timeout time.Duration, onTimeout func(SummaryEvent)) *Watcher {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &Watcher{
		sessions:  make(map[string]*SessionState),
		timeout:   timeout,
		now:       time.Now,
		onTimeout: onTimeout,
	}
}

// SetClock overrides the time source — exposed for deterministic tests.
func (w *Watcher) SetClock(now func() time.Time) {
	if now != nil {
		w.now = now
	}
}

// Heartbeat ingests a payload and updates session state.
func (w *Watcher) Heartbeat(p HeartbeatPayload) error {
	if p.SessionID == "" {
		return errors.New("capture: heartbeat missing session_id")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	s, ok := w.sessions[p.SessionID]
	if !ok {
		s = &SessionState{
			SessionID: p.SessionID,
			Goal:      p.Goal,
			Project:   p.Project,
			Files:     make(map[string]struct{}),
			FirstSeen: w.now(),
		}
		w.sessions[p.SessionID] = s
	}
	if p.Goal != "" {
		s.Goal = p.Goal
	}
	if p.Project != "" {
		s.Project = p.Project
	}
	for _, f := range p.Files {
		if f != "" {
			s.Files[f] = struct{}{}
		}
	}
	s.LastSeen = w.now()
	s.SummaryFired = false // a fresh heartbeat resets the dead-flag
	return nil
}

// Tick scans active sessions and returns SummaryEvents for those whose
// LastSeen is older than the configured timeout. Each session fires its
// summary event at most once until a new heartbeat resurrects it.
func (w *Watcher) Tick() []SummaryEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	cutoff := w.now().Add(-w.timeout)
	var events []SummaryEvent
	for id, s := range w.sessions {
		if s.SummaryFired {
			continue
		}
		if s.LastSeen.Before(cutoff) {
			files := make([]string, 0, len(s.Files))
			for f := range s.Files {
				files = append(files, f)
			}
			ev := SummaryEvent{
				SessionID: id,
				Goal:      s.Goal,
				Project:   s.Project,
				Files:     files,
				Reason:    "heartbeat-timeout",
				Duration:  s.LastSeen.Sub(s.FirstSeen),
			}
			s.SummaryFired = true
			events = append(events, ev)
		}
	}
	if w.onTimeout != nil {
		for _, ev := range events {
			w.onTimeout(ev)
		}
	}
	return events
}

// Sessions returns a snapshot of currently tracked sessions (test/diagnostic helper).
func (w *Watcher) Sessions() []SessionState {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]SessionState, 0, len(w.sessions))
	for _, s := range w.sessions {
		copy := *s
		// shallow-copy is fine; map is intentionally shared for test inspection
		out = append(out, copy)
	}
	return out
}

// HTTPHandler exposes the Watcher over a minimal HTTP surface:
//
//	POST /watch/heartbeat { session_id, goal?, project?, files? }
//	GET  /watch/sessions  → JSON snapshot
func (w *Watcher) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/watch/heartbeat", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var payload HeartbeatPayload
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			http.Error(rw, fmt.Sprintf("invalid payload: %v", err), http.StatusBadRequest)
			return
		}
		if err := w.Heartbeat(payload); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		rw.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/watch/sessions", func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(w.Sessions())
	})
	return mux
}

// Run blocks until ctx is done. It calls Tick every interval and dispatches
// timeouts via the onTimeout callback.
func (w *Watcher) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Tick()
		}
	}
}
