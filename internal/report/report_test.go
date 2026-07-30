package report

import (
	"strings"
	"testing"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/config"
	"github.com/Rnurdaulet/pingbot/internal/store"
)

func TestFormatStatusAndStats(t *testing.T) {
	t.Parallel()

	targets := []config.Target{{Name: "ummet.kz"}, {Name: "sunna.kz"}}
	statuses := map[string]string{"ummet.kz": "up", "sunna.kz": "down"}

	statusMsg := FormatStatus("srv-01", targets, statuses)
	for _, part := range []string{
		"pingbot status · hostname=srv-01 · targets=2",
		"🟢 ummet.kz — up",
		"🔴 sunna.kz — down",
	} {
		if !strings.Contains(statusMsg, part) {
			t.Fatalf("status missing %q\n%s", part, statusMsg)
		}
	}

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-1 * time.Hour)
	incidents := []store.IncidentRow{{
		Target:    "sunna.kz",
		Kind:      "down",
		StartedAt: now.Add(-2 * time.Hour),
		EndedAt:   &ended,
	}}

	statsMsg := FormatStats("srv-01", targets, statuses, incidents, now)
	for _, part := range []string{
		"pingbot stats · hostname=srv-01",
		"🟢 ummet.kz — up",
		"🔴 sunna.kz — down",
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
