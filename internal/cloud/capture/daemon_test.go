package capture

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWatcher_HeartbeatThenTimeout(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	w := NewWatcher(5*time.Minute, nil)
	w.SetClock(func() time.Time { return now })

	if err := w.Heartbeat(HeartbeatPayload{
		SessionID: "s1",
		Goal:      "ship feature",
		Project:   "aria-core",
		Files:     []string{"a.go", "b.go"},
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	// Within timeout window — no events.
	now = now.Add(2 * time.Minute)
	if events := w.Tick(); len(events) != 0 {
		t.Fatalf("no events expected in window, got %d", len(events))
	}

	// Past timeout — fire summary.
	now = now.Add(10 * time.Minute)
	events := w.Tick()
	if len(events) != 1 {
		t.Fatalf("want 1 timeout event, got %d", len(events))
	}
	ev := events[0]
	if ev.SessionID != "s1" || ev.Goal != "ship feature" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if len(ev.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(ev.Files))
	}

	// Tick again — must not refire.
	if events := w.Tick(); len(events) != 0 {
		t.Fatalf("re-tick must not refire, got %d", len(events))
	}
}

func TestWatcher_HeartbeatResurrectsSession(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	w := NewWatcher(5*time.Minute, nil)
	w.SetClock(func() time.Time { return now })

	_ = w.Heartbeat(HeartbeatPayload{SessionID: "s1"})
	now = now.Add(10 * time.Minute)
	w.Tick() // fires summary

	// Fresh heartbeat: clock advanced again, summary should fire again.
	_ = w.Heartbeat(HeartbeatPayload{SessionID: "s1"})
	now = now.Add(10 * time.Minute)
	events := w.Tick()
	if len(events) != 1 {
		t.Fatalf("resurrected session must fire again, got %d", len(events))
	}
}

func TestWatcher_RejectsEmptySessionID(t *testing.T) {
	w := NewWatcher(time.Minute, nil)
	if err := w.Heartbeat(HeartbeatPayload{}); err == nil {
		t.Fatal("expected error for missing session_id")
	}
}

func TestWatcher_HTTPHandler_Heartbeat(t *testing.T) {
	w := NewWatcher(time.Minute, nil)
	srv := httptest.NewServer(w.HTTPHandler())
	defer srv.Close()

	body, _ := json.Marshal(HeartbeatPayload{SessionID: "abc", Goal: "g"})
	resp, err := http.Post(srv.URL+"/watch/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}

	if got := w.Sessions(); len(got) != 1 || got[0].SessionID != "abc" {
		t.Fatalf("session not stored: %+v", got)
	}
}

func TestWatcher_HTTPHandler_RejectsMalformedJSON(t *testing.T) {
	w := NewWatcher(time.Minute, nil)
	srv := httptest.NewServer(w.HTTPHandler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/watch/heartbeat", "application/json",
		strings.NewReader("not json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}
