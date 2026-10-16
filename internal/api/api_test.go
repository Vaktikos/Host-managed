package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/agentclient"
	"github.com/vaktikos/host-managed/internal/auth"
	"github.com/vaktikos/host-managed/internal/config"
	"github.com/vaktikos/host-managed/internal/jobs"
	"github.com/vaktikos/host-managed/internal/store"
)

// fakeAgent is a test double for the node agent. It is only used in tests;
// the real implementation is agentclient.Client.
type fakeAgent struct {
	mu      sync.Mutex
	vms     map[string]*agentapi.VMDetail
	actions []string
	down    bool
}

func newFake() *fakeAgent { return &fakeAgent{vms: map[string]*agentapi.VMDetail{}} }

func (f *fakeAgent) unavailable() error {
	if f.down {
		return fmt.Errorf("%w: dial unix: connection refused", agentclient.ErrUnavailable)
	}
	return nil
}

func (f *fakeAgent) Host(ctx context.Context) (*agentapi.Host, error) {
	if err := f.unavailable(); err != nil {
		return nil, err
	}
	return &agentapi.Host{Hostname: "node1", KVMAvailable: true}, nil
}
func (f *fakeAgent) Interfaces(context.Context) ([]agentapi.Interface, error) { return nil, nil }
func (f *fakeAgent) Networks(context.Context) ([]agentapi.VirtNetwork, error) { return nil, nil }
func (f *fakeAgent) Pools(context.Context) ([]agentapi.StoragePool, error)    { return nil, nil }
func (f *fakeAgent) Volumes(context.Context, string) ([]agentapi.StorageVolume, error) {
	return nil, nil
}
func (f *fakeAgent) DeleteVolume(context.Context, string, string) error { return nil }
func (f *fakeAgent) Upload(ctx context.Context, pool, name string, size int64, body io.Reader) error {
	n, _ := io.Copy(io.Discard, body)
	if n != size {
		return errors.New("short upload")
	}
	return nil
}
func (f *fakeAgent) Download(ctx context.Context, pool string, d agentapi.Download, log func(string), progress func(done, total int64)) error {
	log("downloading " + d.URL)
	progress(50, 100)
	progress(100, 100)
	return nil
}
func (f *fakeAgent) VMs(ctx context.Context) ([]agentapi.VM, error) {
	if err := f.unavailable(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agentapi.VM
	for _, v := range f.vms {
		out = append(out, v.VM)
	}
	return out, nil
}
func (f *fakeAgent) VM(ctx context.Context, name string) (*agentapi.VMDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.vms[name]; ok {
		return v, nil
	}
	return nil, &agentclient.Error{Status: 404, Code: "not_found", Message: "not found"}
}
func (f *fakeAgent) CreateVM(ctx context.Context, spec agentapi.VMCreate, log func(string)) (*agentapi.VMDetail, error) {
	log("creating disk")
	if spec.DiskPool == "full" {
		return nil, &agentclient.Error{Status: 500, Code: "internal", Message: "no space left on device"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v := &agentapi.VMDetail{VM: agentapi.VM{Name: spec.Name, State: "shutoff", VCPUs: spec.VCPUs, MemoryMiB: spec.MemoryMiB}}
	f.vms[spec.Name] = v
	return v, nil
}
func (f *fakeAgent) VMAction(ctx context.Context, name, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, name+":"+action)
	if _, ok := f.vms[name]; !ok {
		return &agentclient.Error{Status: 404, Code: "not_found", Message: "not found"}
	}
	return nil
}
func (f *fakeAgent) VMMeta(context.Context, string, agentapi.VMMeta) error { return nil }
func (f *fakeAgent) VMResources(context.Context, string, agentapi.VMResources) (bool, error) {
	return false, nil
}
func (f *fakeAgent) VMTemplate(context.Context, string, bool) error { return nil }
func (f *fakeAgent) DeleteVM(ctx context.Context, name string, disks bool) ([]string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.vms, name)
	return nil, "", nil
}
func (f *fakeAgent) VMStats(context.Context, string) ([]agentapi.VMStats, error) { return nil, nil }
func (f *fakeAgent) LatestStats(context.Context) (map[string]agentapi.VMStats, error) {
	return nil, nil
}
func (f *fakeAgent) VMGuest(context.Context, string) (*agentapi.GuestInfo, error) {
	return &agentapi.GuestInfo{}, nil
}
func (f *fakeAgent) VNC(context.Context, string) (net.Conn, *bufio.Reader, error) {
	return nil, nil, errors.New("not supported in fake")
}
func (f *fakeAgent) Containers(context.Context) ([]agentapi.Container, error) { return nil, nil }
func (f *fakeAgent) Container(context.Context, string) (*agentapi.Container, error) {
	return nil, &agentclient.Error{Status: 404, Code: "not_found", Message: "not found"}
}
func (f *fakeAgent) CreateContainer(context.Context, agentapi.ContainerCreate, func(string)) (*agentapi.Container, error) {
	return nil, nil
}
func (f *fakeAgent) ContainerAction(context.Context, string, string) error { return nil }
func (f *fakeAgent) ContainerConfig(context.Context, string, agentclient.ContainerConfig) error {
	return nil
}
func (f *fakeAgent) DeleteContainer(context.Context, string) error { return nil }
func (f *fakeAgent) ContainerConsole(context.Context, string) (net.Conn, *bufio.Reader, error) {
	return nil, nil, errors.New("not supported in fake")
}
func (f *fakeAgent) LXCImages(context.Context, string) ([]agentclient.Image, error) { return nil, nil }

type env struct {
	srv   *httptest.Server
	st    *store.Store
	agent *fakeAgent
}

func setup(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Defaults()
	cfg.InsecureHTTP = true
	fa := newFake()
	jr := jobs.New(st, log, 2)
	s := New(cfg, st, fa, jr, log, "test", nil)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	for _, u := range []struct{ name, role string }{{"admin", "admin"}, {"op", "operator"}, {"viewer", "auditor"}} {
		h, _ := auth.HashPassword("password-" + u.name)
		if _, err := st.CreateUser(context.Background(), u.name, h, u.role, "de"); err != nil {
			t.Fatal(err)
		}
	}
	return &env{srv: srv, st: st, agent: fa}
}

type client struct {
	t      *testing.T
	base   string
	hc     *http.Client
	csrf   string
	bearer string
}

func (e *env) client(t *testing.T) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: e.srv.URL, hc: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

func (c *client) login(user, pw string) int {
	code, body := c.do("POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pw})
	if code == 200 {
		c.csrf = body["csrf_token"].(string)
	}
	return code
}

func errCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func TestLoginAndSession(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	if code, _ := c.do("GET", "/api/v1/auth/me", nil); code != 401 {
		t.Fatalf("unauthenticated /me: %d", code)
	}
	if code := c.login("admin", "wrong"); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code := c.login("nobody", "x"); code != 401 {
		t.Fatalf("unknown user: %d", code)
	}
	if code := c.login("admin", "password-admin"); code != 200 {
		t.Fatalf("login: %d", code)
	}
	code, me := c.do("GET", "/api/v1/auth/me", nil)
	if code != 200 || me["user"].(map[string]any)["username"] != "admin" {
		t.Fatalf("me: %d %v", code, me)
	}
	if _, ok := me["user"].(map[string]any)["password_hash"]; ok {
		t.Fatal("password hash exposed")
	}
	if code, _ := c.do("POST", "/api/v1/auth/logout", nil); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := c.do("GET", "/api/v1/auth/me", nil); code != 401 {
		t.Fatalf("session survived logout: %d", code)
	}
	// Failed logins are audited.
	list, _, _ := e.st.AuditLog(context.Background(), store.AuditFilter{Action: "auth.login"})
	if len(list) < 3 {
		t.Fatalf("login audit entries: %d", len(list))
	}
}

func TestCSRFRequired(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.login("admin", "password-admin")
	good := c.csrf
	c.csrf = ""
	if code, body := c.do("POST", "/api/v1/vms/x/actions/start", nil); code != 403 || errCode(body) != "csrf" {
		t.Fatalf("missing CSRF: %d %v", code, body)
	}
	c.csrf = "wrong"
	if code, _ := c.do("POST", "/api/v1/vms/x/actions/start", nil); code != 403 {
		t.Fatalf("wrong CSRF: %d", code)
	}
	c.csrf = good
	if code, _ := c.do("POST", "/api/v1/vms/x/actions/start", nil); code != 404 {
		t.Fatalf("with CSRF: %d", code)
	}
}

func TestRBAC(t *testing.T) {
	e := setup(t)
	e.agent.vms["web"] = &agentapi.VMDetail{VM: agentapi.VM{Name: "web", State: "running"}}
	viewer := e.client(t)
	viewer.login("viewer", "password-viewer")
	if code, _ := viewer.do("GET", "/api/v1/vms", nil); code != 200 {
		t.Fatalf("viewer list: %d", code)
	}
	if code, body := viewer.do("POST", "/api/v1/vms/web/actions/stop", nil); code != 403 || errCode(body) != "forbidden" {
		t.Fatalf("viewer stop: %d", code)
	}
	if code, _ := viewer.do("GET", "/api/v1/users", nil); code != 403 {
		t.Fatalf("viewer users: %d", code)
	}
	op := e.client(t)
	op.login("op", "password-op")
	if code, _ := op.do("POST", "/api/v1/vms/web/actions/stop", nil); code != 204 {
		t.Fatalf("operator stop: %d", code)
	}
	if code, _ := op.do("POST", "/api/v1/vms", map[string]any{"name": "x"}); code != 403 {
		t.Fatalf("operator create: %d", code)
	}
	if code, _ := op.do("DELETE", "/api/v1/vms/web?confirm=web", nil); code != 403 {
		t.Fatalf("operator delete: %d", code)
	}
	if len(e.agent.actions) != 1 || e.agent.actions[0] != "web:stop" {
		t.Fatalf("agent actions: %v", e.agent.actions)
	}
	denied, _, _ := e.st.AuditLog(context.Background(), store.AuditFilter{Action: "access.denied"})
	if len(denied) != 4 {
		t.Fatalf("denied audit entries: %d", len(denied))
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	var last int
	for i := 0; i < 9; i++ {
		last = c.login("op", "wrong")
	}
	if last != 429 {
		t.Fatalf("expected 429 after repeated failures, got %d", last)
	}
	// Even the right password is refused while blocked.
	if code := c.login("op", "password-op"); code != 429 {
		t.Fatalf("blocked user logged in: %d", code)
	}
}

func TestAPITokens(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.login("op", "password-op")
	code, body := c.do("POST", "/api/v1/auth/me/tokens", map[string]any{"name": "ci", "expires_in_days": 1})
	if code != 201 {
		t.Fatalf("create token: %d %v", code, body)
	}
	tok := body["token"].(string)
	api := e.client(t)
	api.bearer = tok
	if code, _ := api.do("GET", "/api/v1/vms", nil); code != 200 {
		t.Fatalf("token auth: %d", code)
	}
	// Token requests do not need CSRF but keep the user's role.
	if code, _ := api.do("POST", "/api/v1/vms", map[string]any{"name": "x"}); code != 403 {
		t.Fatalf("token escalated: %d", code)
	}
	flip := "0"
	if strings.HasSuffix(tok, "0") {
		flip = "1"
	}
	api.bearer = tok[:len(tok)-1] + flip
	if code, _ := api.do("GET", "/api/v1/vms", nil); code != 401 {
		t.Fatalf("tampered token: %d", code)
	}
	id := body["id"].(string)
	if code, _ := c.do("DELETE", "/api/v1/auth/me/tokens/"+id, nil); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	api.bearer = tok
	if code, _ := api.do("GET", "/api/v1/vms", nil); code != 401 {
		t.Fatalf("revoked token works: %d", code)
	}
}

func TestLastAdminProtected(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.login("admin", "password-admin")
	u, _ := e.st.UserByName(context.Background(), "admin")
	path := fmt.Sprintf("/api/v1/users/%d", u.ID)
	if code, body := c.do("PUT", path, map[string]any{"role": "operator"}); code != 409 || errCode(body) != "last_admin" {
		t.Fatalf("demote last admin: %d %v", code, body)
	}
	if code, _ := c.do("DELETE", path, nil); code != 409 {
		t.Fatalf("self delete: %d", code)
	}
	if code, body := c.do("POST", "/api/v1/users", map[string]any{"username": "bob", "password": "short", "role": "operator"}); code != 400 {
		t.Fatalf("weak password: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/v1/users", map[string]any{"username": "bob", "password": "long-enough-pass", "role": "superuser"}); code != 400 {
		t.Fatalf("invalid role: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/users", map[string]any{"username": "bob", "password": "long-enough-pass", "role": "operator"}); code != 201 {
		t.Fatalf("create user: %d", code)
	}
}

func waitJob(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, j := c.do("GET", "/api/v1/jobs/"+id, nil)
		if s := j["status"]; s == "succeeded" || s == "failed" {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job timeout")
	return nil
}

func TestCreateVMJob(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.login("admin", "password-admin")
	spec := map[string]any{"name": "web1", "vcpus": 2, "memory_mib": 2048, "disk_pool": "local", "disk_size_gib": 20, "network": "network:default"}
	code, body := c.do("POST", "/api/v1/vms", spec)
	if code != 202 {
		t.Fatalf("create: %d %v", code, body)
	}
	id := body["job"].(map[string]any)["id"].(string)
	if j := waitJob(t, c, id); j["status"] != "succeeded" {
		t.Fatalf("job: %v", j)
	}
	_, logBody := c.do("GET", "/api/v1/jobs/"+id+"/log", nil)
	_ = logBody
	if code, body := c.do("POST", "/api/v1/vms", spec); code != 400 || errCode(body) != "validation" {
		t.Fatalf("duplicate: %d %v", code, body)
	}

	bad := map[string]any{"name": "bad name!", "vcpus": 2, "memory_mib": 2048, "disk_pool": "local", "disk_size_gib": 20, "network": "network:default"}
	code, body = c.do("POST", "/api/v1/vms", bad)
	if code != 400 || body["error"].(map[string]any)["field"] != "name" {
		t.Fatalf("validation: %d %v", code, body)
	}
	unknown := map[string]any{"name": "x", "shell": "rm -rf /"}
	if code, _ := c.do("POST", "/api/v1/vms", unknown); code != 400 {
		t.Fatalf("unknown field accepted: %d", code)
	}

	// Storage full: the job fails with the real error message.
	spec["name"], spec["disk_pool"] = "web2", "full"
	_, body = c.do("POST", "/api/v1/vms", spec)
	j := waitJob(t, c, body["job"].(map[string]any)["id"].(string))
	if j["status"] != "failed" || !strings.Contains(j["error"].(string), "no space left") {
		t.Fatalf("full storage job: %v", j)
	}

	// Deletion needs the name as confirmation and a stopped VM.
	if code, _ := c.do("DELETE", "/api/v1/vms/web1", nil); code != 400 {
		t.Fatalf("delete without confirm: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/v1/vms/web1?confirm=web1", nil); code != 202 {
		t.Fatalf("delete: %d", code)
	}
}

func TestAgentUnavailable(t *testing.T) {
	e := setup(t)
	e.agent.down = true
	c := e.client(t)
	c.login("admin", "password-admin")
	code, body := c.do("GET", "/api/v1/vms", nil)
	if code != 503 || errCode(body) != "agent_unavailable" {
		t.Fatalf("agent down: %d %v", code, body)
	}
}

func TestUnknownEndpointAndHeaders(t *testing.T) {
	e := setup(t)
	resp, err := http.Get(e.srv.URL + "/api/v1/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, h := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Request-ID"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
}

func TestDownloadJob(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	c.login("admin", "password-admin")
	for _, bad := range []map[string]any{
		{"url": "file:///etc/shadow"},
		{"url": "ftp://example.com/a.iso"},
		{"url": "https://example.com/a.iso", "checksum": "xyz"},
		{"url": "https://example.com/a.iso", "name": "../../etc/x"},
	} {
		if code, body := c.do("POST", "/api/v1/storage/iso/download", bad); code != 400 {
			t.Fatalf("%v accepted: %d %v", bad, code, body)
		}
	}
	code, body := c.do("POST", "/api/v1/storage/iso/download", map[string]any{"url": "https://example.com/path/debian.iso"})
	if code != 202 {
		t.Fatalf("download: %d %v", code, body)
	}
	job := body["job"].(map[string]any)
	if job["target"] != "storage/iso/debian.iso" {
		t.Fatalf("name not derived from URL: %v", job["target"])
	}
	j := waitJob(t, c, job["id"].(string))
	if j["status"] != "succeeded" || j["progress"] != 100.0 {
		t.Fatalf("job: %v", j)
	}
	op := e.client(t)
	op.login("op", "password-op")
	if code, _ := op.do("POST", "/api/v1/storage/iso/download", map[string]any{"url": "https://example.com/a.iso"}); code != 403 {
		t.Fatalf("operator may download: %d", code)
	}
}
