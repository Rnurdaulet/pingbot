package state

import (
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/checker"
	"github.com/Rnurdaulet/pingbot/internal/config"
)

type fakeNotifier struct {
	mu       sync.Mutex
	msgs     []string
	lastHTML bool
	lastBtns [][]Button
}

func (f *fakeNotifier) SendMessage(msg Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, msg.Text)
	f.lastHTML = msg.HTML
	f.lastBtns = msg.Buttons
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

func TestDownAlertUsesHTMLAndButtons(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()

	apply(m, target, fail(), fail(), fail())
	// Capture last queued message via notifier after close.
	m.Close()

	msgs := n.messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 alert, got %v", msgs)
	}
	if !strings.Contains(msgs[0], "<b>svc</b>") || !strings.Contains(msgs[0], "DOWN") {
		t.Fatalf("expected HTML DOWN alert, got %q", msgs[0])
	}
	if len(n.lastBtns) != 1 || len(n.lastBtns[0]) != 2 {
		t.Fatalf("expected Check+Mute buttons, got %#v", n.lastBtns)
	}
	if n.lastBtns[0][0].Data != "c:svc" || n.lastBtns[0][1].Data != "m:1h" {
		t.Fatalf("unexpected button data: %#v", n.lastBtns[0])
	}
}

func TestMuteSuppressesAlerts(t *testing.T) {
	m, n, s := newTestManager()
	target := testTarget()

	m.Mute(time.Hour)
	apply(m, target, fail(), fail(), fail())
	m.Close()

	if msgs := n.messages(); len(msgs) != 0 {
		t.Fatalf("expected muted alerts, got %v", msgs)
	}
	if len(s.opened) != 1 || s.opened[0] != "down" {
		t.Fatalf("mute must still record incidents, got %v", s.opened)
	}
}

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
	if !strings.Contains(msgs[0], "latency") {
		t.Fatalf("expected latency reason, got %q", msgs[0])
	}
	if len(s.opened) != 1 || s.opened[0] != "degraded" {
		t.Fatalf("expected one degraded incident, got %v", s.opened)
	}
}

func TestDegradedUsesResultErrReason(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()

	certSlow := checker.Result{
		Status:  checker.Slow,
		Latency: 50 * time.Millisecond,
		Err:     "ssl cert expires in 5d (2026-08-09)",
	}
	apply(m, target, ok(), certSlow, certSlow, certSlow)
	m.Close()

	msgs := n.messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SLOW alert, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "ssl cert expires in 5d") {
		t.Fatalf("expected cert reason in alert, got %q", msgs[0])
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
	if s.closed != 1 {
		t.Fatalf("expected dangling incidents closed on Unknown→Up, got %d", s.closed)
	}
}

func TestRestore(t *testing.T) {
	downSince := time.Now().Add(-2 * time.Hour)
	slowSince := time.Now().Add(-90 * time.Minute)

	tests := []struct {
		name       string
		status     Status
		since      time.Time
		results    []checker.Result
		wantMsgs   int
		wantSubstr string
		wantClosed int
	}{
		{
			name:     "down still failing",
			status:   Down,
			since:    downSince,
			results:  []checker.Result{fail(), fail(), fail(), fail()},
			wantMsgs: 0,
		},
		{
			name:       "down then recovery",
			status:     Down,
			since:      downSince,
			results:    []checker.Result{ok(), ok()},
			wantMsgs:   1,
			wantSubstr: "down for 2h 0m",
			wantClosed: 1,
		},
		{
			name:     "degraded still slow",
			status:   Degraded,
			since:    slowSince,
			results:  []checker.Result{slow(), slow(), slow()},
			wantMsgs: 0,
		},
		{
			name:       "degraded then recovery",
			status:     Degraded,
			since:      slowSince,
			results:    []checker.Result{ok(), ok()},
			wantMsgs:   1,
			wantSubstr: "was slow for 1h 30m",
			wantClosed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, n, s := newTestManager()
			target := testTarget()
			m.Restore(target.Name, tt.status, tt.since)
			apply(m, target, tt.results...)
			m.Close()

			msgs := n.messages()
			if len(msgs) != tt.wantMsgs {
				t.Fatalf("alerts = %v, want %d", msgs, tt.wantMsgs)
			}
			if tt.wantSubstr != "" && (len(msgs) == 0 || !strings.Contains(msgs[0], tt.wantSubstr)) {
				t.Fatalf("alert %q, want substr %q", msgs, tt.wantSubstr)
			}
			if len(s.opened) != 0 {
				t.Fatalf("opened %v, want none", s.opened)
			}
			if s.closed != tt.wantClosed {
				t.Fatalf("closed %d, want %d", s.closed, tt.wantClosed)
			}
		})
	}
}

func TestRestoredNeedsOkThreshold(t *testing.T) {
	tests := []struct {
		name   string
		status Status
	}{
		{name: "down", status: Down},
		{name: "degraded", status: Degraded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, n, s := newTestManager()
			target := testTarget()
			m.Restore(target.Name, tt.status, time.Now().Add(-time.Hour))
			apply(m, target, ok())
			m.Close()

			if s.closed != 0 || len(s.opened) != 0 {
				t.Fatalf("closed=%d opened=%v", s.closed, s.opened)
			}
			if msgs := n.messages(); len(msgs) != 0 {
				t.Fatalf("expected no alert before ok_threshold, got %v", msgs)
			}
		})
	}
}

func TestRestoredUpDoesNotLookLikeStartup(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()
	m.Restore(target.Name, Up, time.Now())
	apply(m, target, fail(), fail(), fail())
	m.Close()

	msgs := n.messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "DOWN") || strings.Contains(msgs[0], "first check after startup") {
		t.Fatalf("got %v", msgs)
	}
}

func TestRestoreIgnoresOtherStatuses(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()
	m.Restore(target.Name, Unknown, time.Now().Add(-time.Hour))
	m.Restore(target.Name, "nope", time.Now())
	apply(m, target, fail(), fail(), fail())
	m.Close()

	msgs := n.messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "first check after startup") {
		t.Fatalf("got %v", msgs)
	}
}

func TestMuteSummaryAfterUnmute(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()
	target.Name = "a<b&c"

	until := m.Mute(time.Hour)
	info, ok := m.MuteInfo()
	if !ok || info.UnixNano() != until.UnixNano() {
		t.Fatalf("MuteInfo = %v %v, mute until %v", info, ok, until)
	}
	apply(m, target, fail(), fail(), fail())
	m.Unmute()
	if _, ok := m.MuteInfo(); ok {
		t.Fatal("expected MuteInfo to report unmuted")
	}
	// Stale timer callback must not send a second summary.
	m.onMuteDeadline(until.UnixNano())
	m.Close()

	msgs := n.messages()
	if len(msgs) != 1 {
		t.Fatalf("expected one mute summary, got %v", msgs)
	}
	if !n.lastHTML {
		t.Fatal("expected HTML summary")
	}
	if len(n.lastBtns) != 0 {
		t.Fatalf("summary buttons = %#v", n.lastBtns)
	}
	if !strings.Contains(msgs[0], "mute ended") || !strings.Contains(msgs[0], "a&lt;b&amp;c") || !strings.Contains(msgs[0], "down for") {
		t.Fatalf("unexpected summary: %q", msgs[0])
	}
	if strings.Contains(msgs[0], "connection refused") || strings.Contains(msgs[0], "DOWN") {
		t.Fatalf("original alert leaked: %q", msgs[0])
	}
}

func TestMuteSummaryAllTargetsUp(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()
	m.Mute(time.Hour)
	apply(m, target, fail(), fail(), fail(), ok(), ok())
	m.Unmute()
	m.Close()

	msgs := n.messages()
	if len(msgs) != 1 {
		t.Fatalf("expected one summary, got %v", msgs)
	}
	if !strings.Contains(msgs[0], "mute ended") || !strings.Contains(msgs[0], "all targets up") {
		t.Fatalf("unexpected summary: %q", msgs[0])
	}
}

func TestMuteSummaryListsDownAndDegraded(t *testing.T) {
	m, n, _ := newTestManager()
	downT := testTarget()
	downT.Name = "zeta"
	slowT := testTarget()
	slowT.Name = "alpha"

	m.Mute(time.Hour)
	apply(m, downT, fail(), fail(), fail())
	apply(m, slowT, slow(), slow(), slow())
	m.Unmute()
	m.Close()

	msgs := n.messages()
	if len(msgs) != 1 {
		t.Fatalf("expected one summary, got %v", msgs)
	}
	text := msgs[0]
	alpha := strings.Index(text, "alpha")
	zeta := strings.Index(text, "zeta")
	if alpha < 0 || zeta < 0 || alpha > zeta {
		t.Fatalf("expected alpha before zeta, got %q", text)
	}
	if !strings.Contains(text, "slow for") || !strings.Contains(text, "down for") {
		t.Fatalf("unexpected summary: %q", text)
	}
}

func TestUnmuteWithoutSuppressedAlerts(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()
	m.Mute(time.Hour)
	apply(m, target, ok(), ok())
	m.Unmute()
	m.Close()

	if msgs := n.messages(); len(msgs) != 0 {
		t.Fatalf("expected no summary, got %v", msgs)
	}
}

func TestReplacedMuteTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, n, _ := newTestManager()
		defer m.Close()

		target := testTarget()
		m.Mute(time.Minute)
		apply(m, target, fail(), fail(), fail())
		m.Mute(time.Hour)

		time.Sleep(time.Minute)
		synctest.Wait()
		if msgs := n.messages(); len(msgs) != 0 {
			t.Fatalf("replaced mute timer fired early: %v", msgs)
		}

		time.Sleep(time.Hour - time.Minute)
		synctest.Wait()
		msgs := n.messages()
		if len(msgs) != 1 || !strings.Contains(msgs[0], "mute ended") || !strings.Contains(msgs[0], "down for 1h 0m") {
			t.Fatalf("expected summary at the new deadline, got %v", msgs)
		}
	})
}

func TestUnmuteStopsMuteTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, n, _ := newTestManager()
		defer m.Close()

		m.Mute(time.Hour)
		apply(m, testTarget(), fail(), fail(), fail())
		m.Unmute()
		synctest.Wait()

		time.Sleep(time.Hour)
		synctest.Wait()
		msgs := n.messages()
		if len(msgs) != 1 || !strings.Contains(msgs[0], "mute ended") || !strings.Contains(msgs[0], "down for 0s") {
			t.Fatalf("got %v", msgs)
		}
	})
}

func TestMuteEndAfterCloseDoesNotPanic(t *testing.T) {
	m, n, _ := newTestManager()
	target := testTarget()
	until := m.Mute(time.Hour)
	apply(m, target, fail(), fail(), fail())
	m.Close()

	m.onMuteDeadline(until.UnixNano())
	if msgs := n.messages(); len(msgs) != 0 {
		t.Fatalf("closed manager must not summarize, got %v", msgs)
	}
}
