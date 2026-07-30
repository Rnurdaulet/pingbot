package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/config"
	"github.com/Rnurdaulet/pingbot/internal/report"
	"github.com/Rnurdaulet/pingbot/internal/store"
	"github.com/Rnurdaulet/pingbot/internal/telegram"
)

type statusStore interface {
	Statuses() ([]store.StatusRow, error)
	IncidentsSince(since time.Time) ([]store.IncidentRow, error)
}

// runBotCommands long-polls Telegram for /status and /stats in the configured chat.
func runBotCommands(ctx context.Context, tg *telegram.Client, cfg *config.Config, db statusStore) {
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
			// Don't spin hot on network blips.
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

			cmd := telegram.Command(u.Message.Text)
			var reply string
			switch cmd {
			case "status", "start":
				reply = buildStatusReply(cfg, db)
			case "stats", "history":
				reply = buildStatsReply(cfg, db)
			case "help":
				reply = "commands:\n/status — current status\n/stats — uptime 24h / 7d / 30d"
			default:
				continue
			}

			if err := tg.Send(reply); err != nil {
				slog.Error("telegram command reply failed", "cmd", cmd, "error", err)
			}
		}
	}
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
