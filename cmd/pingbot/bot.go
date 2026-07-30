package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/checker"
	"github.com/Rnurdaulet/pingbot/internal/config"
	"github.com/Rnurdaulet/pingbot/internal/report"
	"github.com/Rnurdaulet/pingbot/internal/state"
	"github.com/Rnurdaulet/pingbot/internal/store"
	"github.com/Rnurdaulet/pingbot/internal/telegram"
)

const defaultIncidentLimit = 10

type statusStore interface {
	Statuses() ([]store.StatusRow, error)
	IncidentsSince(since time.Time) ([]store.IncidentRow, error)
	RecentDowns(limit int) ([]store.IncidentRow, error)
}

// runBotCommands long-polls Telegram for chat commands in the configured chat.
func runBotCommands(ctx context.Context, tg *telegram.Client, cfg *config.Config, db statusStore, chk *checker.Checker, manager *state.Manager) {
	offset := 0
	for {
		if ctx.Err() != nil {
			return
		}

		updates, err := tg.GetUpdates(ctx, offset, 30)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			if !errors.Is(err, http.ErrHandlerTimeout) {
				slog.Error("telegram getUpdates failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}

		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.Message == nil || u.Message.Text == "" {
				continue
			}
			if !tg.AllowedChat(u.Message.Chat.ID) {
				continue
			}

			text := u.Message.Text
			cmd := telegram.Command(text)
			args := telegram.Args(text)

			var reply string
			switch cmd {
			case "status", "start":
				reply = buildStatusReply(cfg, db)
			case "stats", "history":
				reply = buildStatsReply(cfg, db)
			case "incidents":
				reply = buildIncidentsReply(db, args)
			case "check":
				reply = buildCheckReply(ctx, cfg, chk, args)
			case "mute":
				reply = buildMuteReply(manager, args)
			case "unmute":
				manager.Unmute()
				reply = "alerts unmuted"
			case "info":
				reply = report.FormatInfo(cfg)
			case "help":
				reply = helpText()
			default:
				continue
			}

			if err := tg.Send(reply); err != nil {
				slog.Error("telegram command reply failed", "cmd", cmd, "error", err)
			}
		}
	}
}

func helpText() string {
	return strings.TrimSpace(`
commands:
/status — current status
/stats — uptime 24h / 7d / 30d
/incidents [N] — last N downs (default 10)
/check <target> — live HTTP check
/info — config summary (secrets redacted)
/mute <duration> — silence alerts (e.g. 1h, 30m)
/unmute — resume alerts
/help — this list
`)
}

func buildStatusReply(cfg *config.Config, db statusStore) string {
	statuses := map[string]string{}
	if rows, err := db.Statuses(); err != nil {
		slog.Error("status lookup failed", "error", err)
		return "pingbot status: failed to read store"
	} else {
		statuses = report.StatusMap(rows)
	}
	return report.FormatStatus(report.Hostname(), cfg.Targets, statuses)
}

func buildStatsReply(cfg *config.Config, db statusStore) string {
	now := time.Now().UTC()
	statuses := map[string]string{}
	if rows, err := db.Statuses(); err != nil {
		slog.Error("stats status lookup failed", "error", err)
		return "pingbot stats: failed to read store"
	} else {
		statuses = report.StatusMap(rows)
	}

	incidents, err := db.IncidentsSince(now.Add(-30 * 24 * time.Hour))
	if err != nil {
		slog.Error("stats incidents lookup failed", "error", err)
		return "pingbot stats: failed to read incidents"
	}
	return report.FormatStats(report.Hostname(), cfg.Targets, statuses, incidents, now)
}

func buildIncidentsReply(db statusStore, args []string) string {
	limit := defaultIncidentLimit
	if len(args) >= 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n <= 0 {
			return "usage: /incidents [N]  (N = positive number)"
		}
		if n > 50 {
			n = 50
		}
		limit = n
	}
	rows, err := db.RecentDowns(limit)
	if err != nil {
		slog.Error("incidents lookup failed", "error", err)
		return "pingbot incidents: failed to read store"
	}
	return report.FormatIncidents(rows, limit)
}

func buildCheckReply(ctx context.Context, cfg *config.Config, chk *checker.Checker, args []string) string {
	if len(args) < 1 {
		return "usage: /check <target>"
	}
	name := args[0]
	t, ok := findTarget(cfg.Targets, name)
	if !ok {
		return fmt.Sprintf("unknown target %q\nuse /status to list names", name)
	}

	r := chk.Check(ctx, t)
	switch r.Status {
	case checker.OK:
		return report.FormatCheck(t.Name, "ok", r.StatusCode, r.Latency, "", t.MaxLatencyMS)
	case checker.Slow:
		return report.FormatCheck(t.Name, "slow", r.StatusCode, r.Latency, "", t.MaxLatencyMS)
	default:
		return report.FormatCheck(t.Name, "fail", r.StatusCode, r.Latency, r.Err, t.MaxLatencyMS)
	}
}

func findTarget(targets []config.Target, name string) (config.Target, bool) {
	for _, t := range targets {
		if strings.EqualFold(t.Name, name) {
			return t, true
		}
	}
	return config.Target{}, false
}

func buildMuteReply(manager *state.Manager, args []string) string {
	if len(args) < 1 {
		if until, ok := manager.MuteInfo(); ok {
			left := time.Until(until).Round(time.Second)
			return fmt.Sprintf("alerts muted until %s UTC (%s left)\nusage: /mute <duration>  e.g. /mute 1h",
				until.UTC().Format("15:04"), report.FormatDuration(left))
		}
		return "alerts are not muted\nusage: /mute <duration>  e.g. /mute 1h"
	}

	d, err := time.ParseDuration(args[0])
	if err != nil || d <= 0 {
		return "usage: /mute <duration>  e.g. 30m, 1h, 2h30m"
	}
	until := manager.Mute(d)
	return fmt.Sprintf("alerts muted for %s (until %s UTC)",
		report.FormatDuration(d), until.UTC().Format("15:04"))
}
