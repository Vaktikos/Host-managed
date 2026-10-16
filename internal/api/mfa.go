package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vaktikos/host-managed/internal/auth"
	"github.com/vaktikos/host-managed/internal/store"
	"github.com/vaktikos/host-managed/internal/totp"
)

// MFA flow: a correct password for a user with TOTP returns a short-lived
// challenge token instead of a session. The session is only created after a
// valid TOTP or recovery code for that challenge.

type mfaChallenge struct {
	userID   int64
	expires  time.Time
	attempts int
}

type mfaChallenges struct {
	mu sync.Mutex
	m  map[string]*mfaChallenge
}

func (c *mfaChallenges) create(userID int64) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*mfaChallenge{}
	}
	for k, v := range c.m {
		if time.Now().After(v.expires) {
			delete(c.m, k)
		}
	}
	tok := auth.RandomToken(32)
	c.m[tok] = &mfaChallenge{userID: userID, expires: time.Now().Add(5 * time.Minute)}
	return tok
}

// take returns the user for a valid challenge and counts the attempt; after
// 5 attempts the challenge is void.
func (c *mfaChallenges) take(tok string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.m[tok]
	if ch == nil || time.Now().After(ch.expires) || ch.attempts >= 5 {
		delete(c.m, tok)
		return 0, false
	}
	ch.attempts++
	return ch.userID, true
}

func (c *mfaChallenges) done(tok string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, tok)
}

const recoveryCodes = 10

func normalizeRecovery(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "-", ""), " ", ""))
}

func (s *Server) totpSecret(ctx context.Context, userID int64, pending bool) (string, *store.TOTPState, error) {
	st, err := s.Store.TOTP(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	sealed := st.Secret
	if pending {
		sealed = st.Pending
	}
	if sealed == "" {
		return "", st, errors.New("no TOTP secret")
	}
	secret, err := s.Secrets.Open(sealed, "totp:"+strconv.FormatInt(userID, 10))
	return secret, st, err
}

// checkSecondFactor validates a TOTP code or consumes a recovery code.
func (s *Server) checkSecondFactor(ctx context.Context, userID int64, code string) (ok bool, usedRecovery bool) {
	code = strings.TrimSpace(code)
	if len(code) == 6 && strings.Trim(code, "0123456789") == "" {
		secret, st, err := s.totpSecret(ctx, userID, false)
		if err != nil {
			return false, false
		}
		if step, ok := totp.Verify(secret, code, time.Now(), st.LastStep); ok && s.Store.UseTOTPStep(ctx, userID, step) {
			return true, false
		}
		return false, false
	}
	if n := normalizeRecovery(code); len(n) == 16 {
		return s.Store.UseRecoveryCode(ctx, userID, hashSecret(n)), true
	}
	return false, false
}

// loginMFA completes a login that required a second factor.
func (s *Server) loginMFA(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"mfa_token"`
		Code  string `json:"code"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.fail(w, r, 400, "validation", "invalid request")
		return
	}
	ip := s.clientIP(r)
	uid, ok := s.mfa.take(body.Token)
	if !ok {
		s.fail(w, r, http.StatusUnauthorized, "mfa_expired", "the sign-in attempt expired; sign in again")
		return
	}
	u, err := s.Store.UserByID(r.Context(), uid)
	if err != nil || u.Disabled {
		s.fail(w, r, http.StatusUnauthorized, "invalid_credentials", "invalid user name or password")
		return
	}
	if b, _ := s.loginUser.Blocked("user:" + u.Username); b {
		s.fail(w, r, http.StatusTooManyRequests, "rate_limited", "too many failed logins, try again later")
		return
	}
	ok, recovery := s.checkSecondFactor(r.Context(), uid, body.Code)
	if !ok {
		s.loginUser.Fail("user:" + u.Username)
		s.loginIP.Fail("ip:" + ip)
		_ = s.Store.Audit(r.Context(), store.AuditEntry{Username: u.Username, IP: ip, Action: "auth.login.mfa", Resource: "user/" + u.Username, Result: "error", CorrelationID: requestID(r), Detail: "invalid second factor"})
		s.fail(w, r, http.StatusUnauthorized, "invalid_mfa", "invalid code")
		return
	}
	s.mfa.done(body.Token)
	detail := "totp"
	if recovery {
		detail = "recovery code (" + strconv.Itoa(s.Store.RecoveryCodesLeft(r.Context(), uid)) + " left)"
	}
	s.startSession(w, r, u, detail)
}

func (s *Server) mfaSetup(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if !auth.VerifyPassword(body.Password, id.User.PasswordHash) {
		valErr(w, r, "password", "password is wrong")
		return
	}
	if id.User.MFAEnabled {
		s.fail(w, r, 409, "conflict", "two-factor authentication is already enabled")
		return
	}
	secret := totp.NewSecret()
	sealed, err := s.Secrets.Seal(secret, "totp:"+strconv.FormatInt(id.User.ID, 10))
	if err == nil {
		err = s.Store.SetTOTPPending(r.Context(), id.User.ID, sealed)
	}
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]string{"secret": secret, "uri": totp.URI(s.Cfg.ProductName, id.User.Username, secret)})
}

func (s *Server) mfaEnable(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	secret, _, err := s.totpSecret(r.Context(), id.User.ID, true)
	if err != nil {
		s.fail(w, r, 409, "conflict", "start the setup first")
		return
	}
	step, ok := totp.Verify(secret, body.Code, time.Now(), 0)
	if !ok {
		valErr(w, r, "code", "code is wrong; check the time on your device")
		return
	}
	var codes, hashes []string
	for i := 0; i < recoveryCodes; i++ {
		c := auth.RandomToken(8) // 16 hex characters
		codes = append(codes, c[:4]+"-"+c[4:8]+"-"+c[8:12]+"-"+c[12:])
		hashes = append(hashes, hashSecret(c))
	}
	err = s.Store.EnableTOTP(r.Context(), id.User.ID, step, hashes)
	s.audit(r, "auth.mfa.enable", "user/"+id.User.Username, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"recovery_codes": codes})
}

func (s *Server) mfaDisable(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if !auth.VerifyPassword(body.Password, id.User.PasswordHash) {
		valErr(w, r, "password", "password is wrong")
		return
	}
	if ok, _ := s.checkSecondFactor(r.Context(), id.User.ID, body.Code); !ok {
		valErr(w, r, "code", "code is wrong")
		return
	}
	err := s.Store.DisableTOTP(r.Context(), id.User.ID)
	s.audit(r, "auth.mfa.disable", "user/"+id.User.Username, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetMFA lets an administrator remove a user's second factor (lost device).
func (s *Server) resetMFA(w http.ResponseWriter, r *http.Request) {
	u := s.userParam(w, r)
	if u == nil {
		return
	}
	err := s.Store.DisableTOTP(r.Context(), u.ID)
	if err == nil {
		err = s.Store.DeleteUserSessions(r.Context(), u.ID, "")
	}
	s.audit(r, "user.mfa.reset", "user/"+u.Username, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
