package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vaktikos/host-managed/internal/auth"
	"github.com/vaktikos/host-managed/internal/store"
)

const sessionCookie = "hm_session"

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]{1,31}$`)

func hashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// require authenticates the request and checks perm (empty = any user).
func (s *Server) require(perm string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := s.authenticate(r)
		if err != nil {
			s.fail(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		// CSRF: cookie sessions must echo the per-session token on unsafe
		// methods. API tokens are not sent automatically by browsers.
		if id.SessionID != "" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			got := r.Header.Get("X-CSRF-Token")
			if subtle.ConstantTimeCompare([]byte(got), []byte(id.CSRF)) != 1 {
				s.fail(w, r, http.StatusForbidden, "csrf", "missing or invalid CSRF token")
				return
			}
		}
		if perm != "" && !auth.Allowed(id.User.Role, perm) {
			s.audit(r.WithContext(context.WithValue(r.Context(), ctxIdentity, id)), "access.denied", r.Method+" "+r.URL.Path, errors.New("forbidden"), "missing permission "+perm)
			s.fail(w, r, http.StatusForbidden, "forbidden", "missing permission: "+perm)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxIdentity, id)))
	})
}

func (s *Server) authenticate(r *http.Request) (*Identity, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return s.tokenAuth(ctx, strings.TrimPrefix(h, "Bearer "))
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || len(c.Value) != 64 {
		return nil, errors.New("no session")
	}
	se, err := s.Store.SessionByToken(ctx, c.Value)
	if err != nil {
		return nil, err
	}
	u, err := s.Store.UserByID(ctx, se.UserID)
	if err != nil || u.Disabled {
		return nil, errors.New("user unavailable")
	}
	if time.Since(se.LastSeen) > time.Minute {
		_ = s.Store.TouchSession(ctx, se.ID, time.Duration(s.Cfg.SessionIdleMinutes)*time.Minute,
			time.Duration(s.Cfg.SessionMaxHours)*time.Hour, se.CreatedAt)
	}
	return &Identity{User: u, SessionID: se.ID, CSRF: se.CSRF}, nil
}

// API tokens have the form hm_<id>_<secret>.
func (s *Server) tokenAuth(ctx context.Context, tok string) (*Identity, error) {
	parts := strings.Split(tok, "_")
	if len(parts) != 3 || parts[0] != "hm" || len(parts[1]) != 16 || len(parts[2]) != 64 {
		return nil, errors.New("malformed token")
	}
	t, hash, err := s.Store.TokenByID(ctx, parts[1])
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(hashSecret(parts[2])), []byte(hash)) != 1 {
		return nil, errors.New("bad token")
	}
	if t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt) {
		return nil, errors.New("token expired")
	}
	u, err := s.Store.UserByID(ctx, t.UserID)
	if err != nil || u.Disabled {
		return nil, errors.New("user unavailable")
	}
	if t.LastUsed == nil || time.Since(*t.LastUsed) > time.Minute {
		s.Store.TouchToken(ctx, t.ID)
	}
	return &Identity{User: u, TokenID: t.ID}, nil
}

type meResponse struct {
	User        *store.User `json:"user"`
	Permissions []string    `json:"permissions"`
	CSRF        string      `json:"csrf_token,omitempty"`
	Product     string      `json:"product"`
	Version     string      `json:"version"`
}

func (s *Server) meBody(id *Identity) meResponse {
	return meResponse{User: id.User, Permissions: auth.Permissions(id.User.Role), CSRF: id.CSRF, Product: s.Cfg.ProductName, Version: s.Version}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.fail(w, r, 400, "validation", "invalid request")
		return
	}
	ip := s.clientIP(r)
	user := strings.ToLower(strings.TrimSpace(body.Username))
	if b, wait := s.loginIP.Blocked("ip:" + ip); b {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		s.fail(w, r, http.StatusTooManyRequests, "rate_limited", "too many failed logins, try again later")
		return
	}
	if b, wait := s.loginUser.Blocked("user:" + user); b {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		s.fail(w, r, http.StatusTooManyRequests, "rate_limited", "too many failed logins, try again later")
		return
	}
	u, err := s.Store.UserByName(r.Context(), user)
	ok := false
	if err == nil {
		ok = auth.VerifyPassword(body.Password, u.PasswordHash) && !u.Disabled
	} else {
		auth.DummyVerify(body.Password)
	}
	if !ok {
		s.loginIP.Fail("ip:" + ip)
		s.loginUser.Fail("user:" + user)
		_ = s.Store.Audit(r.Context(), store.AuditEntry{Username: truncate(user, 64), IP: ip, Action: "auth.login", Resource: "user/" + truncate(user, 64), Result: "error", CorrelationID: requestID(r), Detail: "invalid credentials"})
		s.fail(w, r, http.StatusUnauthorized, "invalid_credentials", "invalid user name or password")
		return
	}
	if u.MFAEnabled {
		// The password was right; the session waits for the second factor.
		writeJSON(w, 200, map[string]any{"mfa_required": true, "mfa_token": s.mfa.create(u.ID)})
		return
	}
	s.loginUser.Reset("user:" + user)
	s.startSession(w, r, u, "")
}

// startSession creates the session cookie after successful authentication.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *store.User, detail string) {
	ip := s.clientIP(r)
	s.loginUser.Reset("user:" + u.Username)
	token, csrf := auth.RandomToken(32), auth.RandomToken(32)
	idle := time.Duration(s.Cfg.SessionIdleMinutes) * time.Minute
	if err := s.Store.CreateSession(r.Context(), token, csrf, u.ID, idle, ip, r.UserAgent()); err != nil {
		s.agentFail(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: !s.Cfg.InsecureHTTP, SameSite: http.SameSiteStrictMode,
	})
	id := &Identity{User: u, CSRF: csrf}
	_ = s.Store.Audit(r.Context(), store.AuditEntry{Username: u.Username, IP: ip, Action: "auth.login", Resource: "user/" + u.Username, Result: "ok", CorrelationID: requestID(r), Detail: detail})
	writeJSON(w, 200, s.meBody(id))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	if id.SessionID != "" {
		_ = s.Store.DeleteSession(r.Context(), id.SessionID)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: !s.Cfg.InsecureHTTP, SameSite: http.SameSiteStrictMode})
	s.audit(r, "auth.logout", "user/"+id.User.Username, nil, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.meBody(identity(r)))
}

func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if !auth.VerifyPassword(body.Current, id.User.PasswordHash) {
		s.audit(r, "user.password", "user/"+id.User.Username, errors.New("wrong current password"), "")
		writeJSON(w, 400, map[string]apiError{"error": {Code: "validation", Field: "current_password", Message: "current password is wrong", RequestID: requestID(r)}})
		return
	}
	if err := auth.CheckPasswordPolicy(body.New); err != nil {
		writeJSON(w, 400, map[string]apiError{"error": {Code: "validation", Field: "new_password", Message: err.Error(), RequestID: requestID(r)}})
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err == nil {
		err = s.Store.SetPassword(r.Context(), id.User.ID, hash)
	}
	if err == nil {
		// Other sessions of this user are signed out.
		err = s.Store.DeleteUserSessions(r.Context(), id.User.ID, id.SessionID)
	}
	s.audit(r, "user.password", "user/"+id.User.Username, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setOwnLanguage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Language string `json:"language"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if body.Language != "de" && body.Language != "en" {
		writeJSON(w, 400, map[string]apiError{"error": {Code: "validation", Field: "language", Message: "must be de or en", RequestID: requestID(r)}})
		return
	}
	if err := s.Store.SetLanguage(r.Context(), identity(r).User.ID, body.Language); err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ownSessions(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	list, err := s.Store.UserSessions(r.Context(), id.User.ID, id.SessionID)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) deleteOwnSession(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	err := s.Store.DeleteSessionByPrefix(r.Context(), id.User.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, 404, "not_found", "session not found")
		return
	}
	s.audit(r, "auth.session.revoke", "user/"+id.User.Username, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ownTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.UserTokens(r.Context(), identity(r).User.ID)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	var body struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 64 {
		writeJSON(w, 400, map[string]apiError{"error": {Code: "validation", Field: "name", Message: "1-64 characters", RequestID: requestID(r)}})
		return
	}
	if body.ExpiresInDays < 0 || body.ExpiresInDays > 3650 {
		writeJSON(w, 400, map[string]apiError{"error": {Code: "validation", Field: "expires_in_days", Message: "0-3650", RequestID: requestID(r)}})
		return
	}
	var exp *time.Time
	if body.ExpiresInDays > 0 {
		t := time.Now().Add(time.Duration(body.ExpiresInDays) * 24 * time.Hour)
		exp = &t
	}
	tid, secret := auth.RandomToken(8), auth.RandomToken(32)
	err := s.Store.CreateToken(r.Context(), tid, id.User.ID, body.Name, hashSecret(secret), exp)
	s.audit(r, "auth.token.create", "token/"+tid, err, body.Name)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	// The secret is shown exactly once.
	writeJSON(w, 201, map[string]any{"id": tid, "name": body.Name, "token": "hm_" + tid + "_" + secret, "expires_at": exp})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	err := s.Store.DeleteToken(r.Context(), id.User.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, 404, "not_found", "token not found")
		return
	}
	s.audit(r, "auth.token.revoke", "token/"+r.PathValue("id"), err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// User management (admin).

func (s *Server) roles(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, auth.Roles()) }

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Users(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, list)
}

func valErr(w http.ResponseWriter, r *http.Request, field, msg string) {
	writeJSON(w, 400, map[string]apiError{"error": {Code: "validation", Field: field, Message: msg, RequestID: requestID(r)}})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Language string `json:"language"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	body.Username = strings.ToLower(strings.TrimSpace(body.Username))
	if !usernameRe.MatchString(body.Username) {
		valErr(w, r, "username", "2-32 characters: lowercase letters, digits, '.', '_', '-'")
		return
	}
	if !auth.ValidRole(body.Role) {
		valErr(w, r, "role", "unknown role")
		return
	}
	if body.Language == "" {
		body.Language = s.Cfg.DefaultLanguage
	}
	if body.Language != "de" && body.Language != "en" {
		valErr(w, r, "language", "must be de or en")
		return
	}
	if err := auth.CheckPasswordPolicy(body.Password); err != nil {
		valErr(w, r, "password", err.Error())
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	u, err := s.Store.CreateUser(r.Context(), body.Username, hash, body.Role, body.Language)
	if errors.Is(err, store.ErrExists) {
		valErr(w, r, "username", "user already exists")
		return
	}
	s.audit(r, "user.create", "user/"+body.Username, err, "role="+body.Role)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 201, u)
}

func (s *Server) userParam(w http.ResponseWriter, r *http.Request) *store.User {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, 404, "not_found", "user not found")
		return nil
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, 404, "not_found", "user not found")
		return nil
	}
	return u
}

// lastAdminGuard prevents removing the last active administrator.
func (s *Server) lastAdminGuard(ctx context.Context, u *store.User) bool {
	if u.Role != "admin" || u.Disabled {
		return false
	}
	n, err := s.Store.CountActiveAdmins(ctx)
	return err != nil || n <= 1
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	u := s.userParam(w, r)
	if u == nil {
		return
	}
	var body struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if body.Role != nil && !auth.ValidRole(*body.Role) {
		valErr(w, r, "role", "unknown role")
		return
	}
	demote := (body.Role != nil && *body.Role != "admin") || (body.Disabled != nil && *body.Disabled)
	if demote && s.lastAdminGuard(r.Context(), u) {
		s.fail(w, r, 409, "last_admin", "the last active administrator cannot be demoted or disabled")
		return
	}
	var err error
	if body.Role != nil {
		err = s.Store.SetRole(r.Context(), u.ID, *body.Role)
	}
	if err == nil && body.Disabled != nil {
		err = s.Store.SetDisabled(r.Context(), u.ID, *body.Disabled)
		if err == nil && *body.Disabled {
			err = s.Store.DeleteUserSessions(r.Context(), u.ID, "")
		}
	}
	detail := ""
	if body.Role != nil {
		detail += "role=" + *body.Role + " "
	}
	if body.Disabled != nil {
		detail += "disabled=" + strconv.FormatBool(*body.Disabled)
	}
	s.audit(r, "user.update", "user/"+u.Username, err, strings.TrimSpace(detail))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	nu, _ := s.Store.UserByID(r.Context(), u.ID)
	writeJSON(w, 200, nu)
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	u := s.userParam(w, r)
	if u == nil {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := auth.CheckPasswordPolicy(body.Password); err != nil {
		valErr(w, r, "password", err.Error())
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err == nil {
		err = s.Store.SetPassword(r.Context(), u.ID, hash)
	}
	if err == nil {
		err = s.Store.DeleteUserSessions(r.Context(), u.ID, "")
	}
	s.audit(r, "user.password.reset", "user/"+u.Username, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u := s.userParam(w, r)
	if u == nil {
		return
	}
	if u.ID == identity(r).User.ID {
		s.fail(w, r, 409, "self_delete", "you cannot delete your own account")
		return
	}
	if s.lastAdminGuard(r.Context(), u) {
		s.fail(w, r, 409, "last_admin", "the last active administrator cannot be deleted")
		return
	}
	err := s.Store.DeleteUser(r.Context(), u.ID)
	s.audit(r, "user.delete", "user/"+u.Username, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
