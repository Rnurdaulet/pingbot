package report

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/config"
	"github.com/Rnurdaulet/pingbot/internal/store"
)

type WindowStats struct {
	Downs        int
	Degradations int
	DownSeconds  int64
	UptimePct    float64
}

var Windows = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

func Aggregate(incidents []store.IncidentRow, now time.Time, window time.Duration) WindowStats {
	windowStart := now.Add(-window)
	var ws WindowStats

	for _, inc := range incidents {
		end := now
		if inc.EndedAt != nil {
			end = inc.EndedAt.UTC()
		}
		if end.Before(windowStart) {
			continue
		}

		switch inc.Kind {
		case "down":
			ws.Downs++
			start := inc.StartedAt.UTC()
			if start.Before(windowStart) {
				start = windowStart
			}
			ws.DownSeconds += int64(end.Sub(start).Seconds())
		case "degraded":
			ws.Degradations++
		}
	}

	total := window.Seconds()
	ws.UptimePct = roundPct((total - float64(ws.DownSeconds)) / total * 100)
	return ws
}

func roundPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	return float64(int(v*1000)) / 1000
}

func StatusEmoji(status string) string {
	switch status {
	case "up":
		return "🟢"
	case "down":
		return "🔴"
	case "degraded":
		return "🟡"
	default:
		return "⚪"
	}
}

func Hostname() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

func StatusMap(rows []store.StatusRow) map[string]string {
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Target] = r.Status
	}
	return out
}

func FormatStatus(host string, targets []config.Target, statuses map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pingbot status · hostname=%s · targets=%d\n", host, len(targets))
	for _, t := range targets {
		st := statuses[t.Name]
		if st == "" {
			st = "unknown"
		}
		fmt.Fprintf(&b, "\n%s %s — %s", StatusEmoji(st), t.Name, st)
	}
	return b.String()
}

func FormatStartup(host string, targets []config.Target, statuses map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pingbot started · hostname=%s · targets=%d\n", host, len(targets))
	for _, t := range targets {
		st := statuses[t.Name]
		if st == "" {
			st = "unknown"
		}
		fmt.Fprintf(&b, "\n%s %s — %s", StatusEmoji(st), t.Name, st)
	}
	return b.String()
}

// FormatStats renders uptime windows 24h / 7d / 30d for each configured target.
func FormatStats(host string, targets []config.Target, statuses map[string]string, incidents []store.IncidentRow, now time.Time) string {
	byTarget := make(map[string][]store.IncidentRow)
	for _, inc := range incidents {
		byTarget[inc.Target] = append(byTarget[inc.Target], inc)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "pingbot stats · hostname=%s\n", host)

	// Stable window order for readable messages.
	order := []string{"24h", "7d", "30d"}
	for _, t := range targets {
		st := statuses[t.Name]
		if st == "" {
			st = "unknown"
		}
		fmt.Fprintf(&b, "\n%s %s — %s", StatusEmoji(st), t.Name, st)
		for _, label := range order {
			ws := Aggregate(byTarget[t.Name], now, Windows[label])
			fmt.Fprintf(&b, "\n  %s: uptime %.2f%% · down %d · slow %d",
				label, ws.UptimePct, ws.Downs, ws.Degradations)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
