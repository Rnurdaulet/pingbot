package state

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/checker"
	"github.com/Rnurdaulet/pingbot/internal/config"
)

type fakeNotifier struct {
	mu   sync.Mutex
	msgs []string
}

func (f *fakeNotifier) Send(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, text)
	return nil
}

func (f *fakeNotifier) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.msgs...)
}

type fakeStore struct {
	mu       sync.Mutex
	statuses []string
	opened   []string // kinds of opened incidents
	closed   int
}

func (f *fakeStore) SetStatus(target, status string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, status)
	return nil
}

func (f *fakeStore) OpenIncident(target, kind, reason string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, kind)
	return nil
}

func (f *fakeStore) CloseIncidents(target string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func testTarget() config.Target {
	return config.Target{
		Name:          "svc",
		FailThreshold: 3,
		OkThreshold:   2,
		MaxLatencyMS:  1000,
	}
}

func newTestManager() (*Manager, *fakeNotifier, *fakeStore) {
	n := &fakeNotifier{}
	s := &fakeStore{}
	return NewManager(n, s), n, s
}

func apply(m *Manager, t config.Target, results ...checker.Result) {
	for _, r := range results {
		m.Apply(t, r)
	}
}

func ok() checker.Result   { return checker.Result{Status: checker.OK, Latency: 100 * time.Millisecond} }
func slow() checker.Result { return checker.Result{Status: checker.Slow, Latency: 2 * time.Second} }
func fail() checker.Result { return checker.Result{Status: checker.Fail, Err: "connection refused"} }

func TestDownAfterFailThreshold(t *testing.T) {
	m, n, s := newTestManager()
	target := testTarget()

	apply(m, target, ok(), fail(), fail(), fail())
	m.Close()

	msgs := n.messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 alert, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "DOWN") || !strings.Contains(msgs[0], "connection refused") {
		t.Fatalf("unexpected alert: %q", msgs[0])
	}
	if len(s.opened) != 1 || s.opened[0] != "down" {
		t.Fatalf("expected one down incident, got %v", s.opened)
	}
}

func TestAntiFlapBelowThreshold(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()

	// Two fails (threshold is 3), then recovery: no alerts at all.
	apply(m, target, ok(), fail(), fail(), ok())
	m.Close()

	if msgs := n.messages(); len(msgs) != 0 {
		t.Fatalf("expected no alerts, got %v", msgs)
	}
}

func TestNoSpamWhileDown(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()

	apply(m, target, fail(), fail(), fail(), fail(), fail(), fail())
	m.Close()

	if msgs := n.messages(); len(msgs) != 1 {
		t.Fatalf("expected exactly 1 DOWN alert, got %d: %v", len(msgs), msgs)
	}
}

func TestRecoveryAfterDown(t *testing.T) {
	m, n, s := newTestManager()
	target := testTarget()

	apply(m, target, fail(), fail(), fail(), ok(), ok())
	m.Close()

	msgs := n.messages()
	if len(msgs) != 2 {
		t.Fatalf("expected DOWN then UP, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[1], "UP") || !strings.Contains(msgs[1], "down for") {
		t.Fatalf("unexpected recovery alert: %q", msgs[1])
	}
	if s.closed == 0 {
		t.Fatal("expected incidents to be closed on recovery")
	}
}

func TestDegradedOnSlowResponses(t *testing.T) {
	m, n, s := newTestManager()
	target := testTarget()

	// Establish up, then slow responses reach fail_threshold.
	apply(m, target, ok(), slow(), slow(), slow())
	m.Close()

	msgs := n.messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SLOW alert, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "SLOW") {
		t.Fatalf("unexpected alert: %q", msgs[0])
	}
	if len(s.opened) != 1 || s.opened[0] != "degraded" {
		t.Fatalf("expected one degraded incident, got %v", s.opened)
	}
}

func TestRecoveryFromDegraded(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()

	apply(m, target, ok(), slow(), slow(), slow(), ok(), ok())
	m.Close()

	msgs := n.messages()
	if len(msgs) != 2 {
		t.Fatalf("expected SLOW then OK, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[1], "back to normal") {
		t.Fatalf("unexpected latency recovery alert: %q", msgs[1])
	}
}

func TestStartupUpIsSilent(t *testing.T) {
	m, n, s := newTestManager()
	target := testTarget()

	apply(m, target, ok(), ok(), ok())
	m.Close()

	if msgs := n.messages(); len(msgs) != 0 {
		t.Fatalf("expected no alerts on healthy startup, got %v", msgs)
	}
	if len(s.statuses) == 0 || s.statuses[0] != "up" {
		t.Fatalf("expected up status persisted, got %v", s.statuses)
	}
}
