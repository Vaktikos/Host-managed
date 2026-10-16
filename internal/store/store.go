// Package store keeps control plane metadata in SQLite (single node). VM and
// container configuration is not stored here; libvirt and LXC remain the
// source of truth for workloads.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrExists = errors.New("already exists")

type Store struct {
	db      *sql.DB
	auditMu sync.Mutex // serialises the audit hash chain
}

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// migrations are applied in order and never edited once released.
var migrations = []string{
	`CREATE TABLE users (
		id INTEGER PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL,
		language TEXT NOT NULL DEFAULT 'de',
		disabled INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		password_changed_at INTEGER NOT NULL
	);
	CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		csrf TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		ip TEXT NOT NULL,
		user_agent TEXT NOT NULL
	);
	CREATE INDEX sessions_user ON sessions(user_id);
	CREATE TABLE api_tokens (
		id TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		secret_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER,
		last_used INTEGER
	);
	CREATE TABLE jobs (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		target TEXT NOT NULL,
		status TEXT NOT NULL,
		username TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		started_at INTEGER,
		finished_at INTEGER,
		error TEXT NOT NULL DEFAULT '',
		params TEXT NOT NULL DEFAULT '{}'
	);
	CREATE INDEX jobs_created ON jobs(created_at DESC);
	CREATE INDEX jobs_target ON jobs(target, created_at DESC);
	CREATE TABLE job_logs (
		id INTEGER PRIMARY KEY,
		job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
		ts INTEGER NOT NULL,
		line TEXT NOT NULL
	);
	CREATE INDEX job_logs_job ON job_logs(job_id, id);
	CREATE TABLE audit (
		id INTEGER PRIMARY KEY,
		ts INTEGER NOT NULL,
		username TEXT NOT NULL,
		ip TEXT NOT NULL,
		action TEXT NOT NULL,
		resource TEXT NOT NULL,
		result TEXT NOT NULL,
		correlation_id TEXT NOT NULL,
		detail TEXT NOT NULL,
		prev_hash TEXT NOT NULL,
		hash TEXT NOT NULL
	);
	CREATE INDEX audit_ts ON audit(ts DESC);
	-- The application never updates or deletes audit rows.
	CREATE TRIGGER audit_no_update BEFORE UPDATE ON audit BEGIN SELECT RAISE(ABORT, 'audit log is append-only'); END;
	CREATE TRIGGER audit_no_delete BEFORE DELETE ON audit BEGIN SELECT RAISE(ABORT, 'audit log is append-only'); END;
	CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
	// 2: measured progress in percent (NULL = not measurable).
	`ALTER TABLE jobs ADD COLUMN progress REAL;`,
	// 3: TOTP (secrets encrypted with the key outside the database) and
	// single-use recovery codes (SHA-256).
	`ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
	ALTER TABLE users ADD COLUMN totp_pending TEXT NOT NULL DEFAULT '';
	ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE recovery_codes (
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		code_hash TEXT NOT NULL,
		used_at INTEGER,
		PRIMARY KEY (user_id, code_hash)
	);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version VALUES (0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this binary supports (%d); refusing to start", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion reports the applied migration count.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v)
	return v, err
}

// Backup writes a consistent copy of the database to path (VACUUM INTO).
func (s *Store) Backup(ctx context.Context, path string) error {
	if strings.ContainsAny(path, "'\x00") {
		return errors.New("invalid backup path")
	}
	_, err := s.db.ExecContext(ctx, `VACUUM INTO '`+path+`'`)
	return err
}

func unix(t time.Time) int64 { return t.Unix() }

func fromUnix(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
