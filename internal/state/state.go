package state

import (
	"fmt"
	"html"
	"log/slog"
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

// Close flushes queued alerts and stops the sender goroutine.
func (m *Manager) Close() {
	close(m.alerts)
	<-m.done
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

// Mute suppresses Telegram alerts until now+d. Status/incidents still update.
func (m *Manager) Mute(d time.Duration) time.Time {
	until := time.Now().Add(d)
	m.mutedUntil.Store(until.UnixNano())
	return until
}

func (m *Manager) Unmute() {
	m.mutedUntil.Store(0)
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

func (m *Manager) notify(msg Message) {
	if m.muted() {
		slog.Info("alert suppressed (muted)", "text", msg.Text)
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
