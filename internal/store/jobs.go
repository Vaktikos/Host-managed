package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Job struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Target     string     `json:"target"`
	Status     string     `json:"status"` // queued, running, succeeded, failed, cancelled
	Username   string     `json:"username"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Error      string     `json:"error"`
	Params     string     `json:"-"`
}

const jobCols = `id, type, target, status, username, created_at, started_at, finished_at, error, params`

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var c int64
	var st, fin sql.NullInt64
	err := row.Scan(&j.ID, &j.Type, &j.Target, &j.Status, &j.Username, &c, &st, &fin, &j.Error, &j.Params)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j.CreatedAt = time.Unix(c, 0).UTC()
	j.StartedAt, j.FinishedAt = fromUnix(st), fromUnix(fin)
	return &j, nil
}

func (s *Store) CreateJob(ctx context.Context, j *Job) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (id, type, target, status, username, created_at, params) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Type, j.Target, j.Status, j.Username, j.CreatedAt.Unix(), j.Params)
	return err
}

func (s *Store) Job(ctx context.Context, id string) (*Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
}

func (s *Store) SetJobRunning(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'running', started_at = ? WHERE id = ? AND status = 'queued'`, time.Now().Unix(), id)
	return err
}

func (s *Store) FinishJob(ctx context.Context, id, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, finished_at = ?, error = ? WHERE id = ?`, status, time.Now().Unix(), errMsg, id)
	return err
}

// FailInterrupted marks jobs that were running when the service stopped.
// Their real outcome is unknown, which the message says explicitly.
func (s *Store) FailInterrupted(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'failed', finished_at = ?, error = 'interrupted: the service stopped while this job was running; check the resource state' WHERE status IN ('running', 'queued')`,
		time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type JobFilter struct {
	Target string
	Status string
	Limit  int
	Offset int
}

func (s *Store) Jobs(ctx context.Context, f JobFilter) ([]Job, int, error) {
	var where []string
	var args []any
	if f.Target != "" {
		where = append(where, "target = ?")
		args = append(args, f.Target)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs`+w+` ORDER BY created_at DESC, rowid DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *j)
	}
	return out, total, rows.Err()
}

func (s *Store) AppendJobLog(ctx context.Context, id, line string) error {
	if len(line) > 8192 {
		line = line[:8192]
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_logs (job_id, ts, line) VALUES (?, ?, ?)`, id, time.Now().UnixMilli(), line)
	return err
}

type LogLine struct {
	ID   int64     `json:"id"`
	Time time.Time `json:"time"`
	Line string    `json:"line"`
}

func (s *Store) JobLog(ctx context.Context, id string, after int64) ([]LogLine, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, line FROM job_logs WHERE job_id = ? AND id > ? ORDER BY id LIMIT 5000`, id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		var ts int64
		if err := rows.Scan(&l.ID, &ts, &l.Line); err != nil {
			return nil, err
		}
		l.Time = time.UnixMilli(ts).UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// PruneJobs removes finished jobs older than the retention period.
func (s *Store) PruneJobs(ctx context.Context, olderThan time.Duration) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE finished_at IS NOT NULL AND finished_at < ?`, time.Now().Add(-olderThan).Unix())
	return err
}

// Audit log with a SHA-256 hash chain: each row commits to its predecessor,
// so edits made directly in the database file become detectable.

type AuditEntry struct {
	ID            int64     `json:"id"`
	Time          time.Time `json:"time"`
	Username      string    `json:"username"`
	IP            string    `json:"ip"`
	Action        string    `json:"action"`
	Resource      string    `json:"resource"`
	Result        string    `json:"result"`
	CorrelationID string    `json:"correlation_id"`
	Detail        string    `json:"detail"`
	prevHash      string
	hash          string
}

func (a *AuditEntry) digest(prev string) string {
	return sha(fmt.Sprintf("%s|%d|%s|%s|%s|%s|%s|%s|%s", prev, a.Time.UnixMilli(), a.Username, a.IP, a.Action, a.Resource, a.Result, a.CorrelationID, a.Detail))
}

func (s *Store) Audit(ctx context.Context, a AuditEntry) error {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	if len(a.Detail) > 4096 {
		a.Detail = a.Detail[:4096]
	}
	a.Time = time.UnixMilli(time.Now().UnixMilli())
	var prev string
	err := s.db.QueryRowContext(ctx, `SELECT hash FROM audit ORDER BY id DESC LIMIT 1`).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO audit (ts, username, ip, action, resource, result, correlation_id, detail, prev_hash, hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Time.UnixMilli(), a.Username, a.IP, a.Action, a.Resource, a.Result, a.CorrelationID, a.Detail, prev, a.digest(prev))
	return err
}

type AuditFilter struct {
	Username string
	Action   string
	Resource string
	Limit    int
	Offset   int
}

func (s *Store) AuditLog(ctx context.Context, f AuditFilter) ([]AuditEntry, int, error) {
	var where []string
	var args []any
	if f.Username != "" {
		where = append(where, "username = ?")
		args = append(args, f.Username)
	}
	if f.Action != "" {
		where = append(where, "action LIKE ?")
		args = append(args, f.Action+"%")
	}
	if f.Resource != "" {
		where = append(where, "resource = ?")
		args = append(args, f.Resource)
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ts, username, ip, action, resource, result, correlation_id, detail FROM audit`+w+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		var ts int64
		if err := rows.Scan(&a.ID, &ts, &a.Username, &a.IP, &a.Action, &a.Resource, &a.Result, &a.CorrelationID, &a.Detail); err != nil {
			return nil, 0, err
		}
		a.Time = time.UnixMilli(ts).UTC()
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// VerifyAudit walks the chain and returns the id of the first broken row, or
// 0 if the chain is intact.
func (s *Store) VerifyAudit(ctx context.Context) (checked int, brokenAt int64, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ts, username, ip, action, resource, result, correlation_id, detail, prev_hash, hash FROM audit ORDER BY id`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	prev := ""
	for rows.Next() {
		var a AuditEntry
		var ts int64
		if err := rows.Scan(&a.ID, &ts, &a.Username, &a.IP, &a.Action, &a.Resource, &a.Result, &a.CorrelationID, &a.Detail, &a.prevHash, &a.hash); err != nil {
			return checked, 0, err
		}
		a.Time = time.UnixMilli(ts)
		if a.prevHash != prev || a.digest(prev) != a.hash {
			return checked, a.ID, nil
		}
		prev = a.hash
		checked++
	}
	return checked, 0, rows.Err()
}
