package report

import (
	"strings"
	"testing"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/config"
	"github.com/Rnurdaulet/pingbot/internal/store"
)

func TestFormatInfoRedactsSecrets(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Server: config.Server{Addr: ":8080", APIToken: "super-secret"},
		Telegram: config.Telegram{
			BotToken: "123:ABC",
			ChatID:   "-100123",
		},
		Heartbeat: config.Heartbeat{URL: "https://hc-ping.com/uuid-secret-here"},
		Storage:   config.Storage{Driver: "postgres", DSN: "postgres://u:p@host/db"},
		Defaults: config.Defaults{
			Interval:      config.Duration(30 * time.Second),
			Timeout:       config.Duration(5 * time.Second),
			FailThreshold: 3,
			OkThreshold:   2,
		},
		Targets: []config.Target{{
			Name:   "api",
			URL:    "https://api.example.com/health?token=secret",
			Method: "GET",
			Headers: map[string]string{
				"Authorization": "Bearer secret",
				"Content-Type":  "application/json",
			},
			Body:          `{"password":"x"}`,
			ExpectStatus:  []int{200},
			Interval:      config.Duration(30 * time.Second),
			Timeout:       config.Duration(5 * time.Second),
			FailThreshold: 3,
			OkThreshold:   2,
			MaxLatencyMS:  1000,
		}},
	}

	msg := FormatInfo(cfg)
	for _, secret := range []string{"super-secret", "123:ABC", "uuid-secret-here", "Bearer secret", "token=secret", "postgres://u:p"} {
		if strings.Contains(msg, secret) {
			t.Fatalf("secret %q leaked in:\n%s", secret, msg)
		}
	}
	for _, part := range []string{
		"chat_id=-100123",
		"bot_token=***",
		"api_token=***",
		"Authorization=***",
		"Content-Type=application/json",
		"GET https://api.example.com/health",
		"body=(set, redacted)",
		"dsn=***",
	} {
		if !strings.Contains(msg, part) {
			t.Fatalf("missing %q in:\n%s", part, msg)
		}
	}
}

func TestFormatIncidentsAndCheck(t *testing.T) {
	t.Parallel()

	ended := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	incidents := []store.IncidentRow{{
		Target:    "site-b",
		Kind:      "down",
		Reason:    "timeout",
		StartedAt: time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
		EndedAt:   &ended,
	}}
	msg := FormatIncidents(incidents, 10)
	for _, part := range []string{"pingbot incidents · last 10 downs", "🔴 site-b down", "timeout", "10:00", "11:00"} {
		if !strings.Contains(msg, part) {
			t.Fatalf("incidents missing %q\n%s", part, msg)
		}
	}

	check := FormatCheck("site-a", "ok", 200, 240*time.Millisecond, "", 5000)
	if !strings.Contains(check, "live check · site-a") || !strings.Contains(check, "🟢 ok") {
		t.Fatalf("unexpected check msg: %s", check)
	}
}

func TestFormatStatusAndStats(t *testing.T) {
	t.Parallel()

	targets := []config.Target{{Name: "site-a"}, {Name: "site-b"}}
	statuses := map[string]string{"site-a": "up", "site-b": "down"}

	statusMsg := FormatStatus("srv-01", targets, statuses)
	for _, part := range []string{
		"pingbot status · hostname=srv-01 · targets=2",
		"🟢 site-a · up",
		"🔴 site-b · down",
	} {
		if !strings.Contains(statusMsg, part) {
			t.Fatalf("status missing %q\n%s", part, statusMsg)
		}
	}

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-1 * time.Hour)
	incidents := []store.IncidentRow{{
		Target:    "site-b",
		Kind:      "down",
		StartedAt: now.Add(-2 * time.Hour),
		EndedAt:   &ended,
	}}

	statsMsg := FormatStats("srv-01", targets, statuses, incidents, now)
	for _, part := range []string{
		"pingbot stats · hostname=srv-01",
		"🟢 site-a · up",
		"🔴 site-b · down",
		"24h:",
		"7d:",
		"30d:",
		"uptime",
	} {
		if !strings.Contains(statsMsg, part) {
			t.Fatalf("stats missing %q\n%s", part, statsMsg)
		}
	}
}
