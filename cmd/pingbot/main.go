package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/api"
	"github.com/Rnurdaulet/pingbot/internal/checker"
	"github.com/Rnurdaulet/pingbot/internal/config"
	"github.com/Rnurdaulet/pingbot/internal/heartbeat"
	"github.com/Rnurdaulet/pingbot/internal/report"
	"github.com/Rnurdaulet/pingbot/internal/state"
	"github.com/Rnurdaulet/pingbot/internal/store"
	"github.com/Rnurdaulet/pingbot/internal/telegram"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if err := run(*configPath); err != nil {
		slog.Error("pingbot exited with error", "error", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := store.Open(cfg.Storage)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	tg := telegram.New(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
	if err := tg.SetMyCommands(telegram.DefaultCommands()); err != nil {
		slog.Warn("telegram setMyCommands failed", "error", err)
	}
	if err := notifyStartup(tg, cfg, db); err != nil {
		return err
	}

	manager := state.NewManager(tg, db)
	restoreState(manager, cfg, db)
	chk := checker.New()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	for _, target := range cfg.Targets {
		wg.Add(1)
		go func(t config.Target) {
			defer wg.Done()
			runTarget(ctx, t, chk, manager)
		}(target)
	}

	if cfg.Heartbeat.URL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			heartbeat.Run(ctx, cfg.Heartbeat.URL, cfg.Heartbeat.Interval.Std())
		}()
	}

	srv := api.NewServer(cfg.Server.Addr, cfg.Server.APIToken, db)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Run(ctx); err != nil {
			slog.Error("http server stopped", "error", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runBotCommands(ctx, tg, cfg, db, chk, manager)
	}()

	slog.Info("pingbot started",
		"targets", len(cfg.Targets),
		"addr", cfg.Server.Addr,
		"heartbeat", cfg.Heartbeat.URL != "",
	)

	<-ctx.Done()
	slog.Info("shutting down")
	wg.Wait()
	// Flush queued alerts before exiting.
	manager.Close()
	return nil
}

// restoreState seeds trackers from the last status and open incidents.
// since is the latest open incident of that kind, or the status row's updated_at.
// A load error is logged and startup continues.
func restoreState(manager *state.Manager, cfg *config.Config, db *store.Store) {
	statuses, err := db.Statuses()
	if err != nil {
		slog.Warn("restore statuses failed", "error", err)
		return
	}
	open, err := db.OpenIncidents()
	if err != nil {
		slog.Warn("restore open incidents failed", "error", err)
		open = nil
	}

	byName := make(map[string]store.StatusRow, len(statuses))
	for _, row := range statuses {
		byName[row.Target] = row
	}

	for _, target := range cfg.Targets {
		row, ok := byName[target.Name]
		if !ok {
			continue
		}
		since := row.UpdatedAt
		var latest time.Time
		found := false
		for _, inc := range open {
			if inc.Target != target.Name || inc.Kind != row.Status {
				continue
			}
			if !found || inc.StartedAt.After(latest) {
				latest = inc.StartedAt
				found = true
			}
		}
		if found {
			since = latest
		}
		manager.Restore(target.Name, state.Status(row.Status), since)
	}
}

func notifyStartup(tg *telegram.Client, cfg *config.Config, db *store.Store) error {
	if !cfg.Telegram.StartupNotifyEnabled() {
		return nil
	}

	statuses := map[string]string{}
	if rows, err := db.Statuses(); err != nil {
		slog.Warn("startup status lookup failed", "error", err)
	} else {
		statuses = report.StatusMap(rows)
	}

	msg := report.FormatStartup(report.Hostname(), cfg.Targets, statuses)
	if err := tg.Send(msg); err != nil {
		if cfg.Telegram.RequireOnStart {
			return fmt.Errorf("telegram require_on_start: %w", err)
		}
		slog.Error("telegram startup notify failed", "error", err)
	}
	return nil
}

func runTarget(ctx context.Context, t config.Target, chk *checker.Checker, manager *state.Manager) {
	ticker := newImmediateTicker(t.Interval.Std())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			result := chk.Check(ctx, t)
			manager.Apply(t, result)
		}
	}
}
