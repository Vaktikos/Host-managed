// Package api is the versioned REST API of the control plane (/api/v1).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path"
	"strings"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/agentclient"
	"github.com/vaktikos/host-managed/internal/auth"
	"github.com/vaktikos/host-managed/internal/config"
	"github.com/vaktikos/host-managed/internal/jobs"
	"github.com/vaktikos/host-managed/internal/store"
)

type Server struct {
	Cfg     config.Config
	Store   *store.Store
	Agent   Agent
	Jobs    *jobs.Runner
	Log     *slog.Logger
	Version string
	WebUI   fs.FS // built frontend; nil disables UI serving

	loginIP   *auth.Limiter
	loginUser *auth.Limiter
}

func New(cfg config.Config, st *store.Store, ag Agent, jr *jobs.Runner, log *slog.Logger, version string, web fs.FS) *Server {
	s := &Server{
		Cfg: cfg, Store: st, Agent: ag, Jobs: jr, Log: log, Version: version, WebUI: web,
		loginIP:   auth.NewLimiter(20, 15*time.Minute),
		loginUser: auth.NewLimiter(8, 15*time.Minute),
	}
	jr.OnFinish = func(j *store.Job) {
		res := "ok"
		if j.Status != "succeeded" {
			res = j.Status
		}
		_ = st.Audit(context.Background(), store.AuditEntry{
			Username: j.Username, Action: j.Type + ".finished", Resource: j.Target, Result: res,
			CorrelationID: j.ID, Detail: j.Error,
		})
	}
	return s
}

type ctxKey int

const (
	ctxIdentity ctxKey = iota
	ctxRequestID
)

// Identity is the authenticated caller.
type Identity struct {
	User      *store.User
	SessionID string // empty for API tokens
	CSRF      string
	TokenID   string
}

func identity(r *http.Request) *Identity {
	id, _ := r.Context().Value(ctxIdentity).(*Identity)
	return id
}

func requestID(r *http.Request) string {
	v, _ := r.Context().Value(ctxRequestID).(string)
	return v
}

// apiError is the uniform error body.
type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Field     string `json:"field,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, kind, msg string) {
	writeJSON(w, code, map[string]apiError{"error": {Code: kind, Message: msg, RequestID: requestID(r)}})
}

// agentFail translates agent errors into API errors.
func (s *Server) agentFail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *agentclient.Error
	var fe *agentapi.FieldError
	switch {
	case errors.As(err, &fe):
		writeJSON(w, 400, map[string]apiError{"error": {Code: "validation", Message: fe.Reason, Field: fe.Field, RequestID: requestID(r)}})
	case errors.As(err, &ae):
		code := ae.Status
		if code < 400 || code > 599 {
			code = 502
		}
		kind := ae.Code
		if kind == "" {
			kind = "agent_error"
		}
		msg := ae.Message
		if ae.Field != "" {
			msg = strings.TrimPrefix(msg, ae.Field+": ")
		}
		writeJSON(w, code, map[string]apiError{"error": {Code: kind, Message: msg, Field: ae.Field, RequestID: requestID(r)}})
	case errors.Is(err, agentclient.ErrUnavailable):
		s.Log.Error("agent unavailable", "err", err, "request_id", requestID(r))
		s.fail(w, r, 503, "agent_unavailable", "The node agent is not reachable. Check the hostmanaged-agent service.")
	default:
		s.Log.Error("request failed", "err", err, "request_id", requestID(r))
		s.fail(w, r, 500, "internal", "Internal error (request "+requestID(r)+")")
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &agentapi.FieldError{Field: "body", Reason: "invalid JSON: " + err.Error()}
	}
	if dec.More() {
		return &agentapi.FieldError{Field: "body", Reason: "unexpected trailing data"}
	}
	return nil
}

// clientIP honours X-Forwarded-For only from configured trusted proxies.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	for _, p := range s.Cfg.TrustedProxies {
		if p.Contains(addr.Unmap()) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				if a, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
					return a.String()
				}
			}
		}
	}
	return addr.Unmap().String()
}

func (s *Server) audit(r *http.Request, action, resource string, err error, detail string) {
	user := "-"
	if id := identity(r); id != nil {
		user = id.User.Username
	}
	res := "ok"
	if err != nil {
		res = "error"
		if detail == "" {
			detail = err.Error()
		}
	}
	if aerr := s.Store.Audit(r.Context(), store.AuditEntry{
		Username: user, IP: s.clientIP(r), Action: action, Resource: resource, Result: res,
		CorrelationID: requestID(r), Detail: detail,
	}); aerr != nil {
		s.Log.Error("audit write failed", "err", aerr)
	}
}

// Handler builds the full HTTP handler including the web UI.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	pub := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, h) }
	priv := func(pattern, perm string, h http.HandlerFunc) { mux.Handle(pattern, s.require(perm, h)) }
	authed := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.require("", h)) }

	pub("GET /api/v1/info", s.info)
	pub("GET /api/v1/health", s.health)
	pub("POST /api/v1/auth/login", s.login)
	authed("POST /api/v1/auth/logout", s.logout)
	authed("GET /api/v1/auth/me", s.me)
	authed("PUT /api/v1/auth/me/password", s.changeOwnPassword)
	authed("PUT /api/v1/auth/me/language", s.setOwnLanguage)
	authed("GET /api/v1/auth/me/sessions", s.ownSessions)
	authed("DELETE /api/v1/auth/me/sessions/{id}", s.deleteOwnSession)
	authed("GET /api/v1/auth/me/tokens", s.ownTokens)
	authed("POST /api/v1/auth/me/tokens", s.createToken)
	authed("DELETE /api/v1/auth/me/tokens/{id}", s.deleteToken)

	priv("GET /api/v1/roles", auth.PermSysUsers, s.roles)
	priv("GET /api/v1/users", auth.PermSysUsers, s.listUsers)
	priv("POST /api/v1/users", auth.PermSysUsers, s.createUser)
	priv("PUT /api/v1/users/{id}", auth.PermSysUsers, s.updateUser)
	priv("PUT /api/v1/users/{id}/password", auth.PermSysUsers, s.resetPassword)
	priv("DELETE /api/v1/users/{id}", auth.PermSysUsers, s.deleteUser)

	priv("GET /api/v1/node", auth.PermSysView, s.node)
	priv("GET /api/v1/node/interfaces", auth.PermNetView, s.interfaces)
	priv("GET /api/v1/networks", auth.PermNetView, s.networks)
	priv("GET /api/v1/storage", auth.PermStorageView, s.pools)
	priv("GET /api/v1/storage/{pool}/volumes", auth.PermStorageView, s.volumes)
	priv("DELETE /api/v1/storage/{pool}/volumes/{vol}", auth.PermStorageDel, s.deleteVolume)
	priv("PUT /api/v1/storage/{pool}/upload/{name}", auth.PermStorageUp, s.upload)

	priv("GET /api/v1/vms", auth.PermVMView, s.listVMs)
	priv("POST /api/v1/vms", auth.PermVMCreate, s.createVM)
	priv("GET /api/v1/vms/{name}", auth.PermVMView, s.getVM)
	priv("DELETE /api/v1/vms/{name}", auth.PermVMDelete, s.deleteVM)
	priv("POST /api/v1/vms/{name}/actions/{action}", auth.PermVMPower, s.vmAction)
	priv("PUT /api/v1/vms/{name}/meta", auth.PermVMConfig, s.vmMeta)
	priv("PUT /api/v1/vms/{name}/resources", auth.PermVMConfig, s.vmResources)
	priv("PUT /api/v1/vms/{name}/template", auth.PermVMConfig, s.vmTemplate)
	priv("GET /api/v1/vms/{name}/stats", auth.PermVMView, s.vmStats)
	priv("GET /api/v1/vms/{name}/guest", auth.PermVMView, s.vmGuest)
	priv("GET /api/v1/vms/{name}/console", auth.PermVMConsole, s.vmConsole)

	priv("GET /api/v1/containers", auth.PermCTView, s.listCTs)
	priv("POST /api/v1/containers", auth.PermCTCreate, s.createCT)
	priv("GET /api/v1/containers/{name}", auth.PermCTView, s.getCT)
	priv("DELETE /api/v1/containers/{name}", auth.PermCTDelete, s.deleteCT)
	priv("POST /api/v1/containers/{name}/actions/{action}", auth.PermCTPower, s.ctAction)
	priv("PUT /api/v1/containers/{name}/config", auth.PermCTConfig, s.ctConfig)
	priv("GET /api/v1/containers/{name}/console", auth.PermCTConsole, s.ctConsole)
	priv("GET /api/v1/lxc/images", auth.PermCTCreate, s.lxcImages)

	priv("GET /api/v1/jobs", auth.PermJobsView, s.listJobs)
	priv("GET /api/v1/jobs/{id}", auth.PermJobsView, s.getJob)
	priv("GET /api/v1/jobs/{id}/log", auth.PermJobsView, s.jobLog)
	authed("POST /api/v1/jobs/{id}/cancel", s.cancelJob)

	priv("GET /api/v1/audit", auth.PermSysAudit, s.auditLog)
	priv("GET /api/v1/audit/verify", auth.PermSysAudit, s.auditVerify)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, 404, "not_found", "unknown API endpoint")
	})
	if s.WebUI != nil {
		mux.Handle("/", s.spa())
	}
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if len(rid) < 8 || len(rid) > 64 || strings.ContainsAny(rid, " \r\n\t") {
			rid = auth.RandomToken(8)
		}
		ctx := context.WithValue(r.Context(), ctxRequestID, rid)
		h := w.Header()
		h.Set("X-Request-ID", rid)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if !s.Cfg.InsecureHTTP {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		next.ServeHTTP(sw, r.WithContext(ctx))
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.Log.Debug("request", "method", r.Method, "path", r.URL.Path, "status", sw.code, "dur", time.Since(start), "request_id", rid)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController and the websocket library reach the
// underlying connection.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	// Public endpoint: no version or host details before login.
	writeJSON(w, 200, map[string]any{"product": s.Cfg.ProductName, "default_language": s.Cfg.DefaultLanguage})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		s.fail(w, r, 503, "database_unavailable", "database not reachable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// spa serves the built frontend and falls back to index.html for client
// side routes.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.WebUI)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := s.WebUI.Open(p); err == nil {
			st, _ := f.Stat()
			f.Close()
			if st != nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		f, err := s.WebUI.Open("index.html")
		if err != nil {
			http.Error(w, "web UI not built", http.StatusNotFound)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.Copy(w, f.(io.Reader))
	})
}
