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

	"github.com/Rnurdaulet/pingbot/internal/report"
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
		_, _ = w.Write([]byte("ok"))
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
			Windows: make(map[string]windowStats, len(report.Windows)),
		}
		for label, d := range report.Windows {
			ws := report.Aggregate(byTarget[st.Target], now, d)
			ts.Windows[label] = windowStats{
				Downs:        ws.Downs,
				Degradations: ws.Degradations,
				DownSeconds:  ws.DownSeconds,
				UptimePct:    ws.UptimePct,
			}
		}
		resp.Targets = append(resp.Targets, ts)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("stats encode failed", "error", err)
	}
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
