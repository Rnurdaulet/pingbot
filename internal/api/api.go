package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/store"
)

type StatsStore interface {
	Statuses() ([]store.StatusRow, error)
	IncidentsSince(since time.Time) ([]store.IncidentRow, error)
}

type Server struct {
	srv      *http.Server
	db       StatsStore
	apiToken string
}

func NewServer(addr, apiToken string, db StatsStore) *Server {
	s := &Server{db: db, apiToken: apiToken}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/stats", s.requireToken(s.handleStats))

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Empty apiToken → open access (for internal networks).
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiToken == "" {
			next(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(s.apiToken)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

type windowStats struct {
	Downs        int     `json:"downs"`
	Degradations int     `json:"degradations"`
	DownSeconds  int64   `json:"down_seconds"`
	UptimePct    float64 `json:"uptime_pct"`
}

type targetStats struct {
	Name    string                 `json:"name"`
	Status  string                 `json:"status"`
	Since   time.Time              `json:"since"`
	Windows map[string]windowStats `json:"windows"`
}

type statsResponse struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Targets     []targetStats `json:"targets"`
}

var statsWindows = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	maxWindow := 30 * 24 * time.Hour

	statuses, err := s.db.Statuses()
	if err != nil {
		httpError(w, err)
		return
	}
	incidents, err := s.db.IncidentsSince(now.Add(-maxWindow))
	if err != nil {
		httpError(w, err)
		return
	}

	byTarget := make(map[string][]store.IncidentRow)
	for _, inc := range incidents {
		byTarget[inc.Target] = append(byTarget[inc.Target], inc)
	}

	resp := statsResponse{GeneratedAt: now, Targets: []targetStats{}}
	for _, st := range statuses {
		ts := targetStats{
			Name:    st.Target,
			Status:  st.Status,
			Since:   st.UpdatedAt,
			Windows: make(map[string]windowStats, len(statsWindows)),
		}
		for label, d := range statsWindows {
			ts.Windows[label] = aggregate(byTarget[st.Target], now, d)
		}
		resp.Targets = append(resp.Targets, ts)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func aggregate(incidents []store.IncidentRow, now time.Time, window time.Duration) windowStats {
	windowStart := now.Add(-window)
	var ws windowStats

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

func httpError(w http.ResponseWriter, err error) {
	slog.Error("stats query failed", "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
