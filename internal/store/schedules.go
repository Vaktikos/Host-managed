package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/vaktikos/host-managed/internal/backup"
)

// BackupSchedule runs backups of a set of guests at fixed times.
type BackupSchedule struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Store   string `json:"store"`
	// Days is a weekday bit mask, bit 0 = Sunday … bit 6 = Saturday.
	Days int    `json:"days"`
	At   string `json:"at"` // HH:MM, server time zone
	// Selection: all, vms, containers or list (Targets: "vm/name", "ct/name").
	Selection string      `json:"selection"`
	Targets   []string    `json:"targets"`
	Mode      string      `json:"mode"` // container backup mode
	Keep      backup.Keep `json:"keep"`
	Notes     string      `json:"notes"`
	CreatedAt time.Time   `json:"created_at"`
	LastRun   *time.Time  `json:"last_run"`
	LastJob   string      `json:"last_job"`
}

const schedCols = `id, name, enabled, store, days, at, selection, targets, mode, keep, notes, created_at, last_run, last_job`

func scanSchedule(row interface{ Scan(...any) error }) (*BackupSchedule, error) {
	var b BackupSchedule
	var targets, keep string
	var created int64
	var last sql.NullInt64
	err := row.Scan(&b.ID, &b.Name, &b.Enabled, &b.Store, &b.Days, &b.At, &b.Selection, &targets, &b.Mode, &keep, &b.Notes, &created, &last, &b.LastJob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(targets), &b.Targets)
	if b.Targets == nil {
		b.Targets = []string{}
	}
	_ = json.Unmarshal([]byte(keep), &b.Keep)
	b.CreatedAt = time.Unix(created, 0).UTC()
	b.LastRun = fromUnix(last)
	return &b, nil
}

func (s *Store) BackupSchedules(ctx context.Context) ([]BackupSchedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+schedCols+` FROM backup_schedules ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackupSchedule{}
	for rows.Next() {
		b, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (s *Store) BackupSchedule(ctx context.Context, id int64) (*BackupSchedule, error) {
	return scanSchedule(s.db.QueryRowContext(ctx, `SELECT `+schedCols+` FROM backup_schedules WHERE id = ?`, id))
}

func (s *Store) CreateBackupSchedule(ctx context.Context, b *BackupSchedule) error {
	t, _ := json.Marshal(b.Targets)
	k, _ := json.Marshal(b.Keep)
	b.CreatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO backup_schedules (name, enabled, store, days, at, selection, targets, mode, keep, notes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.Name, b.Enabled, b.Store, b.Days, b.At, b.Selection, string(t), b.Mode, string(k), b.Notes, unix(b.CreatedAt))
	if isUnique(err) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	b.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateBackupSchedule(ctx context.Context, b *BackupSchedule) error {
	t, _ := json.Marshal(b.Targets)
	k, _ := json.Marshal(b.Keep)
	res, err := s.db.ExecContext(ctx, `UPDATE backup_schedules SET name = ?, enabled = ?, store = ?, days = ?, at = ?, selection = ?, targets = ?, mode = ?, keep = ?, notes = ? WHERE id = ?`,
		b.Name, b.Enabled, b.Store, b.Days, b.At, b.Selection, string(t), b.Mode, string(k), b.Notes, b.ID)
	if isUnique(err) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteBackupSchedule(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM backup_schedules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkScheduleRun records a run (slot is the scheduled time it covers).
func (s *Store) MarkScheduleRun(ctx context.Context, id int64, slot time.Time, job string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backup_schedules SET last_run = ?, last_job = ? WHERE id = ?`, unix(slot), job, id)
	return err
}
