package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type User struct {
	ID                int64     `json:"id"`
	Username          string    `json:"username"`
	PasswordHash      string    `json:"-"`
	Role              string    `json:"role"`
	Language          string    `json:"language"`
	Disabled          bool      `json:"disabled"`
	CreatedAt         time.Time `json:"created_at"`
	PasswordChangedAt time.Time `json:"password_changed_at"`
	MFAEnabled        bool      `json:"mfa_enabled"`
}

const userCols = `id, username, password_hash, role, language, disabled, created_at, password_changed_at, totp_secret <> ''`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created, changed int64
	var disabled int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Language, &disabled, &created, &changed, &u.MFAEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Disabled = disabled != 0
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.PasswordChangedAt = time.Unix(changed, 0).UTC()
	return &u, nil
}

func (s *Store) CreateUser(ctx context.Context, username, hash, role, lang string) (*User, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, language, created_at, password_changed_at) VALUES (?, ?, ?, ?, ?, ?)`,
		username, hash, role, lang, now, now)
	if isUnique(err) {
		return nil, ErrExists
	}
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.UserByID(ctx, id)
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *Store) UserByName(ctx context.Context, name string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, name))
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountActiveAdmins is used to prevent locking out the last administrator.
func (s *Store) CountActiveAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&n)
	return n, err
}

func (s *Store) SetPassword(ctx context.Context, id int64, hash string) error {
	return s.exec1(ctx, `UPDATE users SET password_hash = ?, password_changed_at = ? WHERE id = ?`, hash, time.Now().Unix(), id)
}

func (s *Store) SetRole(ctx context.Context, id int64, role string) error {
	return s.exec1(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, id)
}

func (s *Store) SetLanguage(ctx context.Context, id int64, lang string) error {
	return s.exec1(ctx, `UPDATE users SET language = ? WHERE id = ?`, lang, id)
}

func (s *Store) SetDisabled(ctx context.Context, id int64, disabled bool) error {
	v := 0
	if disabled {
		v = 1
	}
	return s.exec1(ctx, `UPDATE users SET disabled = ? WHERE id = ?`, v, id)
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return s.exec1(ctx, `DELETE FROM users WHERE id = ?`, id)
}

func (s *Store) exec1(ctx context.Context, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Sessions. The id column stores a SHA-256 of the cookie value, so a leaked
// database does not reveal usable session tokens.

type Session struct {
	ID        string
	UserID    int64
	CSRF      string
	CreatedAt time.Time
	ExpiresAt time.Time
	LastSeen  time.Time
	IP        string
	UserAgent string
}

func (s *Store) CreateSession(ctx context.Context, token, csrf string, userID int64, ttl time.Duration, ip, ua string) error {
	now := time.Now()
	if len(ua) > 256 {
		ua = ua[:256]
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, csrf, created_at, expires_at, last_seen, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sha(token), userID, csrf, now.Unix(), now.Add(ttl).Unix(), now.Unix(), ip, ua)
	return err
}

func (s *Store) SessionByToken(ctx context.Context, token string) (*Session, error) {
	var se Session
	var c, e, l int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, csrf, created_at, expires_at, last_seen, ip, user_agent FROM sessions WHERE id = ?`, sha(token)).
		Scan(&se.ID, &se.UserID, &se.CSRF, &c, &e, &l, &se.IP, &se.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	se.CreatedAt, se.ExpiresAt, se.LastSeen = time.Unix(c, 0), time.Unix(e, 0), time.Unix(l, 0)
	if time.Now().After(se.ExpiresAt) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, se.ID)
		return nil, ErrNotFound
	}
	return &se, nil
}

// TouchSession extends an idle-timeout session.
func (s *Store) TouchSession(ctx context.Context, id string, idle time.Duration, maxAge time.Duration, created time.Time) error {
	now := time.Now()
	exp := now.Add(idle)
	if hard := created.Add(maxAge); exp.After(hard) {
		exp = hard
	}
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen = ?, expires_at = ? WHERE id = ?`, now.Unix(), exp.Unix(), id)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, exceptID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, exceptID)
	return err
}

func (s *Store) PruneSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}

type SessionInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	ExpiresAt time.Time `json:"expires_at"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	Current   bool      `json:"current"`
}

func (s *Store) UserSessions(ctx context.Context, userID int64, currentID string) ([]SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, created_at, last_seen, expires_at, ip, user_agent FROM sessions WHERE user_id = ? AND expires_at >= ? ORDER BY last_seen DESC`,
		userID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var si SessionInfo
		var c, l, e int64
		if err := rows.Scan(&si.ID, &c, &l, &e, &si.IP, &si.UserAgent); err != nil {
			return nil, err
		}
		si.CreatedAt, si.LastSeen, si.ExpiresAt = time.Unix(c, 0).UTC(), time.Unix(l, 0).UTC(), time.Unix(e, 0).UTC()
		si.Current = si.ID == currentID
		// Only expose a prefix of the hash as an identifier.
		si.ID = si.ID[:16]
		out = append(out, si)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSessionByPrefix(ctx context.Context, userID int64, prefix string) error {
	if len(prefix) != 16 {
		return ErrNotFound
	}
	return s.exec1(ctx, `DELETE FROM sessions WHERE user_id = ? AND substr(id, 1, 16) = ?`, userID, prefix)
}

// API tokens: "hm_<id>_<secret>"; only a hash of the secret is stored.

type APIToken struct {
	ID        string     `json:"id"`
	UserID    int64      `json:"user_id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	LastUsed  *time.Time `json:"last_used"`
	hash      string
}

func (s *Store) CreateToken(ctx context.Context, id string, userID int64, name, secretHash string, expires *time.Time) error {
	var exp any
	if expires != nil {
		exp = expires.Unix()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, secret_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, userID, name, secretHash, time.Now().Unix(), exp)
	return err
}

func (s *Store) TokenByID(ctx context.Context, id string) (*APIToken, string, error) {
	var t APIToken
	var c int64
	var e, l sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, secret_hash, created_at, expires_at, last_used FROM api_tokens WHERE id = ?`, id).
		Scan(&t.ID, &t.UserID, &t.Name, &t.hash, &c, &e, &l)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	t.CreatedAt = time.Unix(c, 0).UTC()
	t.ExpiresAt, t.LastUsed = fromUnix(e), fromUnix(l)
	return &t, t.hash, nil
}

func (s *Store) TouchToken(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used = ? WHERE id = ?`, time.Now().Unix(), id)
}

func (s *Store) UserTokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, name, created_at, expires_at, last_used FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		var c int64
		var e, l sql.NullInt64
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &c, &e, &l); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(c, 0).UTC()
		t.ExpiresAt, t.LastUsed = fromUnix(e), fromUnix(l)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteToken(ctx context.Context, userID int64, id string) error {
	return s.exec1(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// TOTP state of a user. Secrets are sealed by the caller.
type TOTPState struct {
	Secret   string
	Pending  string
	LastStep int64
}

func (s *Store) TOTP(ctx context.Context, userID int64) (*TOTPState, error) {
	var t TOTPState
	err := s.db.QueryRowContext(ctx, `SELECT totp_secret, totp_pending, totp_last_step FROM users WHERE id = ?`, userID).Scan(&t.Secret, &t.Pending, &t.LastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

func (s *Store) SetTOTPPending(ctx context.Context, userID int64, sealed string) error {
	return s.exec1(ctx, `UPDATE users SET totp_pending = ? WHERE id = ?`, sealed, userID)
}

// EnableTOTP activates the pending secret and replaces the recovery codes.
func (s *Store) EnableTOTP(ctx context.Context, userID int64, step int64, codeHashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET totp_secret = totp_pending, totp_pending = '', totp_last_step = ? WHERE id = ? AND totp_pending <> ''`, step, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)`, userID, h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UseTOTPStep records a used step; it fails if the step is not newer than
// the last one (protects against replay, also across concurrent requests).
func (s *Store) UseTOTPStep(ctx context.Context, userID, step int64) bool {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, step, userID, step)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// UseRecoveryCode consumes a recovery code once.
func (s *Store) UseRecoveryCode(ctx context.Context, userID int64, hash string) bool {
	res, err := s.db.ExecContext(ctx, `UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`, time.Now().Unix(), userID, hash)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (s *Store) RecoveryCodesLeft(ctx context.Context, userID int64) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&n)
	return n
}

func (s *Store) DisableTOTP(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return s.exec1(ctx, `UPDATE users SET totp_secret = '', totp_pending = '', totp_last_step = 0 WHERE id = ?`, userID)
}
