package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/Rnurdaulet/pingbot/internal/config"
)

type Store struct {
	db     *sql.DB
	driver string
}

func Open(cfg config.Storage) (*Store, error) {
	var db *sql.DB
	var err error

	switch cfg.Driver {
	case "sqlite":
		if dir := filepath.Dir(cfg.Path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create data dir: %w", err)
			}
		}
		db, err = sql.Open("sqlite", cfg.Path)
		if err != nil {
			return nil, err
		}
		// SQLite handles one writer at a time; serialize access.
		db.SetMaxOpenConns(1)

	case "postgres":
		db, err = sql.Open("pgx", cfg.DSN)
		if err != nil {
			return nil, err
		}
		if err := db.Ping(); err != nil {
			db.Close()
			return nil, fmt.Errorf("connect to postgres: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported storage driver %q", cfg.Driver)
	}

	s := &Store{db: db, driver: cfg.Driver}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

var migrations = map[string][]string{
	"sqlite": {
		`CREATE TABLE IF NOT EXISTS status (
			target     TEXT PRIMARY KEY,
			status     TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS incidents (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			target     TEXT NOT NULL,
			kind       TEXT NOT NULL,
			reason     TEXT,
			started_at TIMESTAMP NOT NULL,
			ended_at   TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_target ON incidents(target)`,
	},
	"postgres": {
		`CREATE TABLE IF NOT EXISTS status (
			target     TEXT PRIMARY KEY,
			status     TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS incidents (
			id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			target     TEXT NOT NULL,
			kind       TEXT NOT NULL,
			reason     TEXT,
			started_at TIMESTAMPTZ NOT NULL,
			ended_at   TIMESTAMPTZ
		)`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_target ON incidents(target)`,
	},
}

func (s *Store) migrate() error {
	for _, stmt := range migrations[s.driver] {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// rebind converts `?` placeholders to `$1, $2, ...` for postgres.
func (s *Store) rebind(query string) string {
	if s.driver != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Store) SetStatus(target, status string, at time.Time) error {
	_, err := s.db.Exec(s.rebind(`
		INSERT INTO status (target, status, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(target) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at
	`), target, status, at.UTC())
	return err
}

func (s *Store) OpenIncident(target, kind, reason string, at time.Time) error {
	_, err := s.db.Exec(s.rebind(`
		INSERT INTO incidents (target, kind, reason, started_at) VALUES (?, ?, ?, ?)
	`), target, kind, reason, at.UTC())
	return err
}

func (s *Store) CloseIncidents(target string, at time.Time) error {
	_, err := s.db.Exec(s.rebind(`
		UPDATE incidents SET ended_at = ? WHERE target = ? AND ended_at IS NULL
	`), at.UTC(), target)
	return err
}

type StatusRow struct {
	Target    string
	Status    string
	UpdatedAt time.Time
}

type IncidentRow struct {
	Target    string
	Kind      string
	Reason    string
	StartedAt time.Time
	EndedAt   *time.Time
}

func (s *Store) Statuses() ([]StatusRow, error) {
	rows, err := s.db.Query(`SELECT target, status, updated_at FROM status ORDER BY target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StatusRow
	for rows.Next() {
		var r StatusRow
		if err := rows.Scan(&r.Target, &r.Status, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// IncidentsSince includes open incidents that overlap [since, now].
func (s *Store) IncidentsSince(since time.Time) ([]IncidentRow, error) {
	rows, err := s.db.Query(s.rebind(`
		SELECT target, kind, COALESCE(reason, ''), started_at, ended_at
		FROM incidents
		WHERE ended_at IS NULL OR ended_at >= ?
		ORDER BY started_at
	`), since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []IncidentRow
	for rows.Next() {
		var r IncidentRow
		if err := rows.Scan(&r.Target, &r.Kind, &r.Reason, &r.StartedAt, &r.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Close() error {
	return s.db.Close()
}
