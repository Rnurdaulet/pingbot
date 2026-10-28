package state

import (
	"fmt"
	"html"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/checker"
	"github.com/Rnurdaulet/pingbot/internal/config"
)

type Status string

const (
	Unknown  Status = "unknown"
	Up       Status = "up"
	Down     Status = "down"
	Degraded Status = "degraded"
)

type Store interface {
	SetStatus(target string, status string, at time.Time) error
	OpenIncident(target string, kind string, reason string, at time.Time) error
	CloseIncidents(target string, at time.Time) error
}

type tracker struct {
	status    Status
	failCount int
	okCount   int
	slowCount int
	downSince time.Time
	slowSince time.Time
}

type Manager struct {
	mu         sync.Mutex
	targets    map[string]*tracker
	notifier   Notifier
	store      Store
	alerts     chan Message
	done       chan struct{}
	mutedUntil atomic.Int64 // unix nano; 0 = not muted
	suppressed bool         // an alert was dropped during the current mute
	muteTimer  *time.Timer
	closed     bool
}

func NewManager(notifier Notifier, store Store) *Manager {
	m := &Manager{
		targets:  make(map[string]*tracker),
		notifier: notifier,
		store:    store,
		alerts:   make(chan Message, 64),
		done:     make(chan struct{}),
	}
	go m.senderLoop()
	return m
}

// senderLoop delivers alerts sequentially outside the state lock, so a
// slow Telegram API call never blocks status processing of other targets.
// A single goroutine preserves alert ordering (down before up).
func (m *Manager) senderLoop() {
	defer close(m.done)
	for msg := range m.alerts {
		if err := m.notifier.SendMessage(msg); err != nil {
			slog.Error("telegram send failed", "error", err)
		}
	}
}

// Close stops the mute timer, flushes queued alerts, and stops the sender.
func (m *Manager) Close() {
	m.mu.Lock()
	m.stopMuteTimerLocked()
	m.closed = true
	m.mu.Unlock()

	close(m.alerts)
	<-m.done
}

// Restore seeds a tracker before checks start.
// Down keeps downSince, degraded keeps slowSince, up is healthy.
// Any other status is ignored and the target stays unknown.
func (m *Manager) Restore(target string, status Status, since time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tr := &tracker{}
	switch status {
	case Down:
		tr.status = Down
		tr.downSince = since
	case Degraded:
		tr.status = Degraded
		tr.slowSince = since
	case Up:
		tr.status = Up
	default:
		return
	}
	m.targets[target] = tr
}

func (m *Manager) Apply(t config.Target, r checker.Result) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tr, ok := m.targets[t.Name]
	if !ok {
		tr = &tracker{status: Unknown}
		m.targets[t.Name] = tr
	}

	switch r.Status {
	case checker.Fail:
		tr.failCount++
		tr.okCount = 0
		tr.slowCount = 0
		if tr.status != Down && tr.failCount >= t.FailThreshold {
			m.transitionDown(t, tr, r)
		}

	case checker.Slow:
		tr.slowCount++
		tr.okCount = 0
		tr.failCount = 0
		switch tr.status {
		case Down:
			if tr.slowCount >= t.OkThreshold {
				m.transitionUpFromDown(t, tr, r)
				m.transitionDegraded(t, tr, r)
			}
		case Up, Unknown:
			if tr.slowCount >= t.FailThreshold {
				m.transitionDegraded(t, tr, r)
			}
		}

	case checker.OK:
		tr.okCount++
		tr.failCount = 0
		tr.slowCount = 0
		switch tr.status {
		case Down:
			if tr.okCount >= t.OkThreshold {
				m.transitionUpFromDown(t, tr, r)
			}
		case Degraded:
			if tr.okCount >= t.OkThreshold {
				m.transitionUpFromDegraded(t, tr, r)
			}
		case Unknown:
			tr.status = Up
			m.persistStatus(t.Name, Up)
			m.closeIncidents(t.Name)
		}
	}
}

func (m *Manager) transitionDown(t config.Target, tr *tracker, r checker.Result) {
	wasUnknown := tr.status == Unknown
	tr.status = Down
	tr.downSince = time.Now()

	m.persistStatus(t.Name, Down)
	m.closeIncidents(t.Name)
	if err := m.store.OpenIncident(t.Name, string(Down), r.Err, tr.downSince); err != nil {
		slog.Error("store open incident", "target", t.Name, "error", err)
	}

	msg := fmt.Sprintf("🔴 <b>%s</b> DOWN\n%s", html.EscapeString(t.Name), html.EscapeString(r.Err))
	if wasUnknown {
		msg += "\n(first check after startup)"
	}
	m.notify(Message{
		Text:    msg,
		HTML:    true,
		Buttons: downButtons(t.Name),
	})
}

func (m *Manager) transitionUpFromDown(t config.Target, tr *tracker, r checker.Result) {
	downFor := time.Since(tr.downSince).Round(time.Second)
	tr.status = Up

	m.persistStatus(t.Name, Up)
	m.closeIncidents(t.Name)

	m.notify(Message{
		Text: fmt.Sprintf("🟢 <b>%s</b> UP\ndown for %s\nlatency %dms",
			html.EscapeString(t.Name), formatDuration(downFor), r.Latency.Milliseconds()),
		HTML: true,
	})
}

func (m *Manager) transitionDegraded(t config.Target, tr *tracker, r checker.Result) {
	tr.status = Degraded
	tr.slowSince = time.Now()

	m.persistStatus(t.Name, Degraded)
	reason := r.Err
	if reason == "" {
		reason = fmt.Sprintf("latency %dms > %dms", r.Latency.Milliseconds(), t.MaxLatencyMS)
	}
	if err := m.store.OpenIncident(t.Name, string(Degraded), reason, tr.slowSince); err != nil {
		slog.Error("store open incident", "target", t.Name, "error", err)
	}

	m.notify(Message{
		Text: fmt.Sprintf("🟡 <b>%s</b> SLOW\n%s", html.EscapeString(t.Name), html.EscapeString(reason)),
		HTML: true,
	})
}

func (m *Manager) transitionUpFromDegraded(t config.Target, tr *tracker, r checker.Result) {
	slowFor := time.Since(tr.slowSince).Round(time.Second)
	tr.status = Up

	m.persistStatus(t.Name, Up)
	m.closeIncidents(t.Name)

	m.notify(Message{
		Text: fmt.Sprintf("🟢 <b>%s</b> OK\nlatency back to normal: %dms (was slow for %s)",
			html.EscapeString(t.Name), r.Latency.Milliseconds(), formatDuration(slowFor)),
		HTML: true,
	})
}

// downButtons builds inline actions for a DOWN alert.
// Callback payloads must stay ≤64 bytes (Telegram limit).
func downButtons(target string) [][]Button {
	row := []Button{{Label: "Mute 1h", Data: "m:1h"}}
	checkData := "c:" + target
	if len(checkData) <= 64 {
		row = append([]Button{{Label: "Check", Data: checkData}}, row...)
	}
	return [][]Button{row}
}

func (m *Manager) persistStatus(target string, s Status) {
	if err := m.store.SetStatus(target, string(s), time.Now()); err != nil {
		slog.Error("store set status", "target", target, "error", err)
	}
}

func (m *Manager) closeIncidents(target string) {
	if err := m.store.CloseIncidents(target, time.Now()); err != nil {
		slog.Error("store close incidents", "target", target, "error", err)
	}
}

// Mute suppresses Telegram alerts until now+d. Status and incidents still update.
// Alerts dropped while muted are summarized once when the mute ends.
func (m *Manager) Mute(d time.Duration) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()

	until := time.Now().Add(d)
	deadline := until.UnixNano()
	m.mutedUntil.Store(deadline)
	m.stopMuteTimerLocked()
	m.muteTimer = time.AfterFunc(d, func() { m.onMuteDeadline(deadline) })
	return until
}

// Unmute resumes alerts. If any were suppressed, one summary is queued.
func (m *Manager) Unmute() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mutedUntil.Store(0)
	m.stopMuteTimerLocked()
	m.flushSuppressedLocked()
}

func (m *Manager) onMuteDeadline(deadline int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mutedUntil.Load() != deadline {
		return
	}
	m.mutedUntil.Store(0)
	// Already running; don't Stop from inside the callback.
	m.muteTimer = nil
	m.flushSuppressedLocked()
}

func (m *Manager) stopMuteTimerLocked() {
	if m.muteTimer != nil {
		m.muteTimer.Stop()
		m.muteTimer = nil
	}
}

// flushSuppressedLocked queues one summary when an alert was dropped.
// Caller holds m.mu. A newer mute (mutedUntil still set) keeps the flag.
func (m *Manager) flushSuppressedLocked() {
	if m.closed || !m.suppressed || m.muted() {
		return
	}
	m.suppressed = false
	m.enqueueLocked(m.muteSummaryLocked())
}

func (m *Manager) muteSummaryLocked() Message {
	names := make([]string, 0, len(m.targets))
	for name, tr := range m.targets {
		if tr.status == Down || tr.status == Degraded {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("🔔 mute ended")
	if len(names) == 0 {
		b.WriteString("\nall targets up")
	} else {
		for _, name := range names {
			tr := m.targets[name]
			switch tr.status {
			case Down:
				fmt.Fprintf(&b, "\n🔴 <b>%s</b> down for %s",
					html.EscapeString(name), formatDuration(time.Since(tr.downSince)))
			case Degraded:
				fmt.Fprintf(&b, "\n🟡 <b>%s</b> slow for %s",
					html.EscapeString(name), formatDuration(time.Since(tr.slowSince)))
			}
		}
	}
	return Message{Text: b.String(), HTML: true}
}

// MuteInfo returns the mute deadline when alerts are currently suppressed.
func (m *Manager) MuteInfo() (until time.Time, muted bool) {
	nanos := m.mutedUntil.Load()
	if nanos == 0 {
		return time.Time{}, false
	}
	until = time.Unix(0, nanos)
	if time.Now().After(until) {
		return time.Time{}, false
	}
	return until, true
}

func (m *Manager) muted() bool {
	_, ok := m.MuteInfo()
	return ok
}

// notify delivers an alert, or remembers that it was dropped while muted.
// Caller holds m.mu.
func (m *Manager) notify(msg Message) {
	if m.muted() {
		m.suppressed = true
		slog.Info("alert suppressed (muted)", "text", msg.Text)
		return
	}
	m.enqueueLocked(msg)
}

// enqueueLocked queues an alert. Caller holds m.mu.
// After Close the send is skipped so a late timer cannot panic.
func (m *Manager) enqueueLocked(msg Message) {
	if m.closed {
		return
	}
	select {
	case m.alerts <- msg:
	default:
		slog.Warn("alert queue full, dropping alert", "text", msg.Text)
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	mnt := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, mnt)
	case mnt > 0:
		return fmt.Sprintf("%dm %ds", mnt, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
