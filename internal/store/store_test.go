package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/config"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(config.Storage{
		Driver: "sqlite",
		Path:   filepath.Join(t.TempDir(), "pingbot.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenIncidents(t *testing.T) {
	s := openTestStore(t)

	empty, err := s.OpenIncidents()
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected no open incidents, got %+v", empty)
	}

	t0 := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	t2 := t0.Add(2 * time.Hour)

	// Insert out of started_at order, then close some.
	if err := s.OpenIncident("b", "down", "refused", t1); err != nil {
		t.Fatal(err)
	}
	if err := s.OpenIncident("a", "degraded", "slow", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseIncidents("a", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.OpenIncident("a", "down", "timeout", t2); err != nil {
		t.Fatal(err)
	}
	if err := s.OpenIncident("c", "down", "gone", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseIncidents("c", t2); err != nil {
		t.Fatal(err)
	}

	got, err := s.OpenIncidents()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 open incidents, got %+v", got)
	}
	if got[0].Target != "b" || got[0].Kind != "down" || got[0].Reason != "refused" || got[0].EndedAt != nil {
		t.Fatalf("first = %+v", got[0])
	}
	if !got[0].StartedAt.Equal(t1) {
		t.Fatalf("b started_at = %v, want %v", got[0].StartedAt, t1)
	}
	if got[1].Target != "a" || got[1].Kind != "down" || got[1].Reason != "timeout" || got[1].EndedAt != nil {
		t.Fatalf("second = %+v", got[1])
	}
	if !got[1].StartedAt.Equal(t2) {
		t.Fatalf("a started_at = %v, want %v", got[1].StartedAt, t2)
	}
}
