package report

import (
	"fmt"
	"net/url"
	"os"
	"sort"
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

func FormatDuration(d time.Duration) string {
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

func FormatIncidents(incidents []store.IncidentRow, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pingbot incidents · last %d downs\n", limit)
	if len(incidents) == 0 {
		b.WriteString("\n(none)")
		return b.String()
	}
	for _, inc := range incidents {
		end := "open"
		dur := time.Since(inc.StartedAt).Round(time.Second)
		if inc.EndedAt != nil {
			end = inc.EndedAt.UTC().Format("15:04")
			dur = inc.EndedAt.Sub(inc.StartedAt).Round(time.Second)
		}
		fmt.Fprintf(&b, "\n🔴 %s down\n  %s → %s (%s)",
			inc.Target,
			inc.StartedAt.UTC().Format("2006-01-02 15:04"),
			end,
			FormatDuration(dur),
		)
		if inc.Reason != "" {
			fmt.Fprintf(&b, "\n  %s", inc.Reason)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatInfo renders a redacted config summary safe for Telegram.
func FormatInfo(cfg *config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pingbot info · hostname=%s\n", Hostname())

	fmt.Fprintf(&b, "\nserver: %s", cfg.Server.Addr)
	if cfg.Server.APIToken != "" {
		b.WriteString(" · api_token=***")
	} else {
		b.WriteString(" · api_token=(empty)")
	}

	fmt.Fprintf(&b, "\ntelegram: chat_id=%s · bot_token=*** · startup_notify=%v · require_on_start=%v",
		cfg.Telegram.ChatID,
		cfg.Telegram.StartupNotifyEnabled(),
		cfg.Telegram.RequireOnStart,
	)

	if cfg.Heartbeat.URL != "" {
		fmt.Fprintf(&b, "\nheartbeat: %s · interval=%s", RedactURL(cfg.Heartbeat.URL), cfg.Heartbeat.Interval.Std())
	} else {
		b.WriteString("\nheartbeat: (off)")
	}

	switch cfg.Storage.Driver {
	case "postgres":
		b.WriteString("\nstorage: postgres · dsn=***")
	default:
		fmt.Fprintf(&b, "\nstorage: sqlite · path=%s", cfg.Storage.Path)
	}

	fmt.Fprintf(&b, "\ndefaults: interval=%s · timeout=%s · fail=%d · ok=%d",
		cfg.Defaults.Interval.Std(),
		cfg.Defaults.Timeout.Std(),
		cfg.Defaults.FailThreshold,
		cfg.Defaults.OkThreshold,
	)

	fmt.Fprintf(&b, "\n\ntargets (%d):", len(cfg.Targets))
	for _, t := range cfg.Targets {
		fmt.Fprintf(&b, "\n• %s\n  %s %s", t.Name, t.Method, RedactURL(t.URL))
		if len(t.ExpectStatus) > 0 {
			fmt.Fprintf(&b, "\n  expect=%v", t.ExpectStatus)
		}
		fmt.Fprintf(&b, "\n  interval=%s · timeout=%s · fail=%d · ok=%d",
			t.Interval.Std(), t.Timeout.Std(), t.FailThreshold, t.OkThreshold)
		if t.MaxLatencyMS > 0 {
			fmt.Fprintf(&b, " · max_latency=%dms", t.MaxLatencyMS)
		}
		if hdrs := RedactHeaders(t.Headers); len(hdrs) > 0 {
			keys := make([]string, 0, len(hdrs))
			for k := range hdrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, fmt.Sprintf("%s=%s", k, hdrs[k]))
			}
			fmt.Fprintf(&b, "\n  headers: %s", strings.Join(parts, ", "))
		}
		if t.Body != "" {
			b.WriteString("\n  body=(set, redacted)")
		}
	}
	return b.String()
}

func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return "***"
	}
	if u.User != nil {
		u.User = url.UserPassword("***", "***")
	}
	// Drop query/fragment — often contains tokens.
	u.RawQuery = ""
	u.Fragment = ""
	if u.Path != "" && u.Path != "/" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, p := range parts {
			// Long path segments are often tokens/UUIDs (e.g. healthchecks.io).
			if len(p) >= 16 {
				parts[i] = "***"
			}
		}
		u.Path = "/" + strings.Join(parts, "/")
	}
	return u.String()
}

func RedactHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if sensitiveHeader(k) {
			out[k] = "***"
			continue
		}
		out[k] = v
	}
	return out
}

func sensitiveHeader(name string) bool {
	n := strings.ToLower(name)
	switch {
	case n == "authorization", n == "proxy-authorization":
		return true
	case strings.Contains(n, "token"), strings.Contains(n, "secret"), strings.Contains(n, "password"), strings.Contains(n, "api-key"), strings.Contains(n, "apikey"), strings.Contains(n, "cookie"):
		return true
	default:
		return false
	}
}

func FormatCheck(name string, status string, statusCode int, latency time.Duration, errText string, maxLatencyMS int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "live check · %s\n\n", name)
	switch status {
	case "ok":
		fmt.Fprintf(&b, "🟢 ok · %d · %dms", statusCode, latency.Milliseconds())
	case "slow":
		fmt.Fprintf(&b, "🟡 slow · %d · %dms (max %dms)", statusCode, latency.Milliseconds(), maxLatencyMS)
	default:
		fmt.Fprintf(&b, "🔴 fail · %dms", latency.Milliseconds())
		if errText != "" {
			fmt.Fprintf(&b, "\n%s", errText)
		}
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
