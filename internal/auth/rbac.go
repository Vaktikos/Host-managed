package auth

import (
	"sort"
	"sync"
	"time"
)

// Permission names. The control plane checks them on every request; the
// frontend only uses them to hide controls.
const (
	PermSysView     = "sys.view"
	PermSysAudit    = "sys.audit"
	PermSysUsers    = "sys.users"
	PermJobsView    = "jobs.view"
	PermVMView      = "vm.view"
	PermVMPower     = "vm.power"
	PermVMConsole   = "vm.console"
	PermVMConfig    = "vm.config"
	PermVMCreate    = "vm.create"
	PermVMDelete    = "vm.delete"
	PermCTView      = "ct.view"
	PermCTPower     = "ct.power"
	PermCTConsole   = "ct.console"
	PermCTConfig    = "ct.config"
	PermCTCreate    = "ct.create"
	PermCTDelete    = "ct.delete"
	PermStorageView = "storage.view"
	PermStorageUp   = "storage.upload"
	PermStorageDel  = "storage.delete"
	PermNetView     = "network.view"
)

var allPerms = []string{
	PermSysView, PermSysAudit, PermSysUsers, PermJobsView,
	PermVMView, PermVMPower, PermVMConsole, PermVMConfig, PermVMCreate, PermVMDelete,
	PermCTView, PermCTPower, PermCTConsole, PermCTConfig, PermCTCreate, PermCTDelete,
	PermStorageView, PermStorageUp, PermStorageDel, PermNetView,
}

// Role definitions. Scoping permissions to individual resources (ACL paths
// like /vms/web1) is planned; today a role applies to the whole node.
var roles = map[string][]string{
	"admin": allPerms,
	"vm-admin": {PermSysView, PermJobsView, PermNetView, PermStorageView, PermStorageUp,
		PermVMView, PermVMPower, PermVMConsole, PermVMConfig, PermVMCreate, PermVMDelete,
		PermCTView, PermCTPower, PermCTConsole, PermCTConfig, PermCTCreate, PermCTDelete},
	"operator": {PermSysView, PermJobsView, PermNetView, PermStorageView,
		PermVMView, PermVMPower, PermVMConsole, PermCTView, PermCTPower, PermCTConsole},
	"storage-admin": {PermSysView, PermJobsView, PermStorageView, PermStorageUp, PermStorageDel, PermVMView, PermCTView},
	"network-admin": {PermSysView, PermJobsView, PermNetView, PermVMView, PermCTView},
	"auditor": {PermSysView, PermSysAudit, PermJobsView, PermNetView, PermStorageView, PermVMView, PermCTView},
}

var roleSets = func() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for r, ps := range roles {
		out[r] = map[string]bool{}
		for _, p := range ps {
			out[r][p] = true
		}
	}
	return out
}()

func ValidRole(r string) bool { _, ok := roles[r]; return ok }

func Allowed(role, perm string) bool { return roleSets[role][perm] }

func Permissions(role string) []string {
	out := append([]string(nil), roles[role]...)
	sort.Strings(out)
	return out
}

type RoleInfo struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

func Roles() []RoleInfo {
	var out []RoleInfo
	for r := range roles {
		out = append(out, RoleInfo{Name: r, Permissions: Permissions(r)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Limiter is a fixed-window failure counter used against brute force. Keys
// are client IPs and user names; both are checked so that neither spraying
// one account from many IPs nor many accounts from one IP works.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	start time.Time
	n     int
}

func NewLimiter(max int, w time.Duration) *Limiter {
	return &Limiter{max: max, window: w, hits: map[string]*window{}}
}

// Blocked reports whether key exceeded the limit and for how long.
func (l *Limiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.hits[key]
	if w == nil {
		return false, 0
	}
	if time.Since(w.start) > l.window {
		delete(l.hits, key)
		return false, 0
	}
	if w.n >= l.max {
		return true, l.window - time.Since(w.start)
	}
	return false, 0
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.hits[key]
	if w == nil || time.Since(w.start) > l.window {
		w = &window{start: time.Now()}
		l.hits[key] = w
	}
	w.n++
	if len(l.hits) > 100000 {
		// Bound memory under a distributed attack.
		for k, v := range l.hits {
			if time.Since(v.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
