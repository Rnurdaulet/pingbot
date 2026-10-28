package heartbeat

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"
)

func Run(ctx context.Context, url string, interval time.Duration) {
	client := &http.Client{Timeout: 10 * time.Second}

	ping := func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			slog.Error("heartbeat build request", "error", err)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			slog.Warn("heartbeat ping failed", "error", err)
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
		resp.Body.Close()
	}

	ping()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ping()
		}
	}
}
