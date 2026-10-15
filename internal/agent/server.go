// Package agent is the privileged node agent. It runs as root, listens on a
// UNIX socket (single node) and exposes only the typed operations defined in
// agentapi. It never runs commands supplied by its caller.
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/hostinfo"
	"github.com/vaktikos/host-managed/internal/lxc"
	"github.com/vaktikos/host-managed/internal/virt"
)

type Server struct {
	Virt    *virt.Manager
	LXC     *lxc.Manager
	CPU     *hostinfo.CPUSampler
	Version string
	Log     *slog.Logger
	// AllowedUIDs may connect besides root (the control plane's user).
	AllowedUIDs []uint32
	// MaxUpload limits ISO uploads (bytes).
	MaxUpload int64

	locks sync.Map // resource name -> *sync.Mutex, serialises mutating ops
}

type ctxKey int

const peerKey ctxKey = 1

// Listen creates the socket with restrictive permissions.
func Listen(path string, gid int) (net.Listener, error) {
	_ = os.Remove(path)
	old := syscall.Umask(0o117)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if gid >= 0 {
		if err := os.Chown(path, 0, gid); err != nil {
			ln.Close()
			return nil, err
		}
	}
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, peerKey, c)
		},
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// peerAllowed checks SO_PEERCRED so that only root and the control plane
// user can talk to the agent even if socket permissions are loosened.
func (s *Server) peerAllowed(r *http.Request) bool {
	c, ok := r.Context().Value(peerKey).(net.Conn)
	if !ok {
		return true // not a socket connection (tests via httptest)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return true
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	var cred *syscall.Ucred
	var cerr error
	_ = raw.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if cerr != nil || cred == nil {
		return false
	}
	if cred.Uid == 0 {
		return true
	}
	for _, u := range s.AllowedUIDs {
		if cred.Uid == u {
			return true
		}
	}
	return false
}

func (s *Server) lock(key string) func() {
	v, _ := s.locks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// TryLock returns false if a mutating operation already runs on key.
func (s *Server) tryLock(key string) (func(), bool) {
	v, _ := s.locks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	h := func(pattern string, fn http.HandlerFunc) { mux.HandleFunc(pattern, fn) }

	h("GET /v1/host", s.host)
	h("GET /v1/interfaces", s.interfaces)
	h("GET /v1/networks", s.networks)
	h("GET /v1/pools", s.pools)
	h("GET /v1/pools/{pool}/volumes", s.volumes)
	h("DELETE /v1/pools/{pool}/volumes/{vol}", s.deleteVolume)
	h("PUT /v1/pools/{pool}/upload/{name}", s.upload)

	h("GET /v1/vms", s.listVMs)
	h("POST /v1/vms", s.createVM)
	h("GET /v1/vms/{name}", s.getVM)
	h("DELETE /v1/vms/{name}", s.deleteVM)
	h("POST /v1/vms/{name}/action", s.vmAction)
	h("PUT /v1/vms/{name}/meta", s.vmMeta)
	h("PUT /v1/vms/{name}/resources", s.vmResources)
	h("PUT /v1/vms/{name}/template", s.vmTemplate)
	h("GET /v1/vms/{name}/stats", s.vmStats)
	h("GET /v1/vms/{name}/guest", s.vmGuest)
	h("GET /v1/vms/{name}/vnc", s.vmVNC)
	h("GET /v1/stats/latest", s.latestStats)

	h("GET /v1/containers", s.listCTs)
	h("POST /v1/containers", s.createCT)
	h("GET /v1/containers/{name}", s.getCT)
	h("DELETE /v1/containers/{name}", s.deleteCT)
	h("POST /v1/containers/{name}/action", s.ctAction)
	h("PUT /v1/containers/{name}/config", s.ctConfig)
	h("GET /v1/containers/{name}/console", s.ctConsole)
	h("GET /v1/lxc/images", s.lxcImages)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.peerAllowed(r) {
			writeErr(w, http.StatusForbidden, "forbidden", "peer not allowed")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, agentapi.Error{Code: kind, Message: msg})
}

// fail maps adapter errors to HTTP status codes.
func fail(w http.ResponseWriter, err error) {
	var fe *agentapi.FieldError
	switch {
	case errors.As(err, &fe):
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "validation", "message": fe.Error(), "field": fe.Field})
	case errors.Is(err, virt.ErrNotFound), errors.Is(err, lxc.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, virt.ErrConflict), errors.Is(err, lxc.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &agentapi.FieldError{Field: "body", Reason: err.Error()}
	}
	return nil
}

func (s *Server) host(w http.ResponseWriter, r *http.Request) {
	h := hostinfo.Collect(s.CPU)
	h.LibvirtVersion, h.QEMUVersion = s.Virt.Versions()
	h.LXCVersion = s.LXC.Version(r.Context())
	h.AgentVersion = s.Version
	writeJSON(w, 200, h)
}

func (s *Server) interfaces(w http.ResponseWriter, r *http.Request) {
	ifs, err := hostinfo.Interfaces()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, ifs)
}

func (s *Server) networks(w http.ResponseWriter, r *http.Request) {
	n, err := s.Virt.ListNetworks()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, n)
}

func (s *Server) pools(w http.ResponseWriter, r *http.Request) {
	p, err := s.Virt.ListPools()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) volumes(w http.ResponseWriter, r *http.Request) {
	v, err := s.Virt.ListVolumes(r.PathValue("pool"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	defer s.lock("pool:" + r.PathValue("pool"))()
	if err := s.Virt.DeleteVolume(r.PathValue("pool"), r.PathValue("vol")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// upload streams a file (ISO image) into a directory pool. The file is
// written under a temporary name and renamed only when complete.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	pool, name := r.PathValue("pool"), r.PathValue("name")
	target, err := s.Virt.UploadTarget(pool, name)
	if err != nil {
		fail(w, err)
		return
	}
	limit := s.MaxUpload
	if limit <= 0 {
		limit = 64 << 30
	}
	if r.ContentLength > limit {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds upload limit")
		return
	}
	tmp := target + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		fail(w, fmt.Errorf("%w: upload already in progress or not writable", virt.ErrConflict))
		return
	}
	n, err := io.Copy(f, io.LimitReader(r.Body, limit+1))
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err == nil && n > limit {
		err = fmt.Errorf("file exceeds upload limit")
	}
	if err == nil && r.ContentLength >= 0 && n != r.ContentLength {
		err = fmt.Errorf("incomplete upload: %d of %d bytes", n, r.ContentLength)
	}
	if err != nil {
		os.Remove(tmp)
		fail(w, err)
		return
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		fail(w, err)
		return
	}
	_ = s.Virt.RefreshPool(pool)
	writeJSON(w, 201, map[string]any{"path": target, "size": n})
}

func (s *Server) listVMs(w http.ResponseWriter, r *http.Request) {
	v, err := s.Virt.ListVMs()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) getVM(w http.ResponseWriter, r *http.Request) {
	v, err := s.Virt.GetVM(r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}

// streamer writes NDJSON progress lines for long operations.
type streamer struct {
	mu sync.Mutex
	w  http.ResponseWriter
	f  http.Flusher
}

func newStreamer(w http.ResponseWriter) *streamer {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(200)
	f, _ := w.(http.Flusher)
	return &streamer{w: w, f: f}
}

func (st *streamer) send(v any) {
	st.mu.Lock()
	defer st.mu.Unlock()
	_ = json.NewEncoder(st.w).Encode(v)
	if st.f != nil {
		st.f.Flush()
	}
}

func (st *streamer) log(line string) { st.send(map[string]string{"log": line}) }

func (st *streamer) finish(result any, err error) {
	if err != nil {
		code := "internal"
		var fe *agentapi.FieldError
		switch {
		case errors.As(err, &fe):
			code = "validation"
		case errors.Is(err, virt.ErrNotFound), errors.Is(err, lxc.ErrNotFound):
			code = "not_found"
		case errors.Is(err, virt.ErrConflict), errors.Is(err, lxc.ErrConflict):
			code = "conflict"
		}
		st.send(map[string]any{"error": agentapi.Error{Code: code, Message: err.Error()}})
		return
	}
	st.send(map[string]any{"result": result})
}

func (s *Server) createVM(w http.ResponseWriter, r *http.Request) {
	var c agentapi.VMCreate
	if err := decode(r, &c); err != nil {
		fail(w, err)
		return
	}
	if err := c.Validate(); err != nil {
		fail(w, err)
		return
	}
	unlock, ok := s.tryLock("vm:" + c.Name)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "another operation on this VM is in progress")
		return
	}
	defer unlock()
	st := newStreamer(w)
	vm, err := s.Virt.CreateVM(c, st.log)
	st.finish(vm, err)
}

func (s *Server) deleteVM(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	unlock, ok := s.tryLock("vm:" + name)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "another operation on this VM is in progress")
		return
	}
	defer unlock()
	removed, err := s.Virt.DeleteVM(name, r.URL.Query().Get("disks") == "1")
	if err != nil && removed == nil {
		fail(w, err)
		return
	}
	resp := map[string]any{"removed_disks": removed}
	if err != nil {
		resp["warning"] = err.Error()
	}
	writeJSON(w, 200, resp)
}

func (s *Server) vmAction(w http.ResponseWriter, r *http.Request) {
	var a agentapi.Action
	if err := decode(r, &a); err != nil {
		fail(w, err)
		return
	}
	name := r.PathValue("name")
	unlock, ok := s.tryLock("vm:" + name)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "another operation on this VM is in progress")
		return
	}
	defer unlock()
	if err := s.Virt.VMAction(name, a.Action); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmMeta(w http.ResponseWriter, r *http.Request) {
	var m agentapi.VMMeta
	if err := decode(r, &m); err != nil {
		fail(w, err)
		return
	}
	defer s.lock("vm:" + r.PathValue("name"))()
	if err := s.Virt.SetMeta(r.PathValue("name"), m); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmResources(w http.ResponseWriter, r *http.Request) {
	var res agentapi.VMResources
	if err := decode(r, &res); err != nil {
		fail(w, err)
		return
	}
	defer s.lock("vm:" + r.PathValue("name"))()
	pending, err := s.Virt.SetResources(r.PathValue("name"), res)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"pending_restart": pending})
}

func (s *Server) vmTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Template bool `json:"template"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	defer s.lock("vm:" + r.PathValue("name"))()
	if err := s.Virt.SetTemplate(r.PathValue("name"), body.Template); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Virt.VMStats(r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) latestStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Virt.LatestStats())
}

func (s *Server) vmGuest(w http.ResponseWriter, r *http.Request) {
	gi, err := s.Virt.GuestInfo(r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, gi)
}

// hijack takes over the connection after a 101 response so that raw bytes
// can flow in both directions.
func hijack(w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("connection cannot be hijacked")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: hm-stream\r\n\r\n")
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, rw, nil
}

func (s *Server) vmVNC(w http.ResponseWriter, r *http.Request) {
	path, err := s.Virt.VNCSocket(r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	vnc, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		fail(w, fmt.Errorf("connect to VNC: %w", err))
		return
	}
	defer vnc.Close()
	conn, rw, err := hijack(w)
	if err != nil {
		return
	}
	defer conn.Close()
	pipe(conn, rw.Reader, vnc)
}

// pipe copies in both directions until either side closes.
func pipe(client net.Conn, clientReader io.Reader, backend net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(backend, clientReader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, backend); done <- struct{}{} }()
	<-done
}

func (s *Server) listCTs(w http.ResponseWriter, r *http.Request) {
	c, err := s.LXC.List(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) getCT(w http.ResponseWriter, r *http.Request) {
	c, err := s.LXC.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) createCT(w http.ResponseWriter, r *http.Request) {
	var c agentapi.ContainerCreate
	if err := decode(r, &c); err != nil {
		fail(w, err)
		return
	}
	if err := c.Validate(); err != nil {
		fail(w, err)
		return
	}
	unlock, ok := s.tryLock("ct:" + c.Name)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "another operation on this container is in progress")
		return
	}
	defer unlock()
	st := newStreamer(w)
	// Detach from the request context: a dropped control plane connection
	// must not leave a half-created container behind.
	ct, err := s.LXC.Create(context.WithoutCancel(r.Context()), c, st.log)
	st.finish(ct, err)
}

func (s *Server) deleteCT(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	unlock, ok := s.tryLock("ct:" + name)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "another operation on this container is in progress")
		return
	}
	defer unlock()
	if err := s.LXC.Delete(r.Context(), name); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ctAction(w http.ResponseWriter, r *http.Request) {
	var a agentapi.Action
	if err := decode(r, &a); err != nil {
		fail(w, err)
		return
	}
	name := r.PathValue("name")
	unlock, ok := s.tryLock("ct:" + name)
	if !ok {
		writeErr(w, http.StatusConflict, "locked", "another operation on this container is in progress")
		return
	}
	defer unlock()
	if err := s.LXC.Action(r.Context(), name, a.Action); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ctConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MemoryMiB   uint64  `json:"memory_mib"`
		SwapMiB     uint64  `json:"swap_mib"`
		CPULimit    float64 `json:"cpu_limit"`
		Autostart   *bool   `json:"autostart"`
		Description *string `json:"description"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	name := r.PathValue("name")
	defer s.lock("ct:" + name)()
	if err := s.LXC.SetLimits(name, body.MemoryMiB, body.SwapMiB, body.CPULimit, body.Autostart, body.Description); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) lxcImages(w http.ResponseWriter, r *http.Request) {
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = "amd64"
	}
	imgs, err := lxc.ListImages(r.Context(), arch)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, imgs)
}
