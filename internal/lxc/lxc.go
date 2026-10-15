// Package lxc manages system containers with the LXC userspace tools. Every
// command is run with a fixed argument vector; no shell is involved and all
// user-supplied values are validated before they reach an argument.
package lxc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// Runner executes a program; replaced in tests.
type Runner func(ctx context.Context, stdin []byte, name string, args ...string) (stdout, stderr []byte, err error)

func execRunner(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	if p := os.Getenv("HTTPS_PROXY"); p != "" {
		// The download template fetches images; honour a configured proxy.
		cmd.Env = append(cmd.Env, "HTTPS_PROXY="+p, "https_proxy="+p, "HTTP_PROXY="+p, "http_proxy="+p)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

type Manager struct {
	Path    string // container directory, default /var/lib/lxc
	Run     Runner
	IDMap   IDMap
	CGroup2 bool
}

// IDMap is the subordinate ID range used for unprivileged containers.
type IDMap struct {
	UIDStart, UIDCount uint32
	GIDStart, GIDCount uint32
}

func New() *Manager {
	m := &Manager{Path: "/var/lib/lxc", Run: execRunner}
	m.IDMap = readIDMap()
	_, err := os.Stat("/sys/fs/cgroup/cgroup.controllers")
	m.CGroup2 = err == nil
	return m
}

// readIDMap takes root's first subordinate range, falling back to the LXC
// convention of 100000:65536.
func readIDMap() IDMap {
	m := IDMap{100000, 65536, 100000, 65536}
	parse := func(path string) (uint32, uint32, bool) {
		b, err := os.ReadFile(path)
		if err != nil {
			return 0, 0, false
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(strings.TrimSpace(line), ":")
			if len(f) == 3 && f[0] == "root" {
				s, e1 := strconv.ParseUint(f[1], 10, 32)
				c, e2 := strconv.ParseUint(f[2], 10, 32)
				if e1 == nil && e2 == nil && c >= 65536 {
					return uint32(s), 65536, true
				}
			}
		}
		return 0, 0, false
	}
	if s, c, ok := parse("/etc/subuid"); ok {
		m.UIDStart, m.UIDCount = s, c
	}
	if s, c, ok := parse("/etc/subgid"); ok {
		m.GIDStart, m.GIDCount = s, c
	}
	return m
}

func (m *Manager) run(ctx context.Context, args ...string) (string, error) {
	return m.runIn(ctx, nil, args...)
}

func (m *Manager) runIn(ctx context.Context, stdin []byte, args ...string) (string, error) {
	out, errb, err := m.Run(ctx, stdin, args[0], args[1:]...)
	if err != nil {
		msg := strings.TrimSpace(string(errb))
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return string(out), fmt.Errorf("%s: %v: %s", args[0], err, msg)
	}
	return string(out), nil
}

func (m *Manager) dir(name string) string { return filepath.Join(m.Path, name) }

func (m *Manager) exists(name string) bool {
	_, err := os.Stat(filepath.Join(m.dir(name), "config"))
	return err == nil
}

func (m *Manager) check(name string) error {
	if !agentapi.ValidContainerName(name) {
		return fmt.Errorf("%w: invalid container name", ErrNotFound)
	}
	if !m.exists(name) {
		return fmt.Errorf("%w: container %q", ErrNotFound, name)
	}
	return nil
}

// sidecar holds Host-managed metadata next to the LXC config.
type sidecar struct {
	Description  string `json:"description,omitempty"`
	Distribution string `json:"distribution,omitempty"`
	Release      string `json:"release,omitempty"`
}

func (m *Manager) readSidecar(name string) sidecar {
	var s sidecar
	b, err := os.ReadFile(filepath.Join(m.dir(name), "hostmanaged.json"))
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func (m *Manager) writeSidecar(name string, s sidecar) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(m.dir(name), "hostmanaged.json"), b, 0o600)
}

// parseConfig returns key/value pairs of an LXC config (last value wins,
// except for repeated keys which are joined with newlines).
func parseConfig(data string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if prev, ok := out[k]; ok && k == "lxc.idmap" {
			v = prev + "\n" + v
		}
		out[k] = v
	}
	return out
}

func (m *Manager) List(ctx context.Context) ([]agentapi.Container, error) {
	ents, err := os.ReadDir(m.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return []agentapi.Container{}, nil
		}
		return nil, err
	}
	out := []agentapi.Container{}
	for _, e := range ents {
		if !e.IsDir() || !agentapi.ValidContainerName(e.Name()) || !m.exists(e.Name()) {
			continue
		}
		c, err := m.Get(ctx, e.Name())
		if err != nil {
			continue
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) Get(ctx context.Context, name string) (*agentapi.Container, error) {
	if err := m.check(name); err != nil {
		return nil, err
	}
	cfgRaw, err := os.ReadFile(filepath.Join(m.dir(name), "config"))
	if err != nil {
		return nil, err
	}
	cfg := parseConfig(string(cfgRaw))
	sc := m.readSidecar(name)
	c := &agentapi.Container{
		Name:         name,
		State:        "unknown",
		Autostart:    cfg["lxc.start.auto"] == "1",
		Unprivileged: cfg["lxc.idmap"] != "",
		Hostname:     cfg["lxc.uts.name"],
		Distribution: sc.Distribution,
		Release:      sc.Release,
		Description:  sc.Description,
		IPs:          []string{},
	}
	if v := cfg["lxc.cgroup2.memory.max"]; v != "" {
		c.MemoryLimit, _ = strconv.ParseUint(v, 10, 64)
	} else if v := cfg["lxc.cgroup.memory.limit_in_bytes"]; v != "" {
		c.MemoryLimit, _ = strconv.ParseUint(v, 10, 64)
	}
	if v := cfg["lxc.cgroup2.cpu.max"]; v != "" {
		if f := strings.Fields(v); len(f) == 2 && f[0] != "max" {
			q, _ := strconv.ParseFloat(f[0], 64)
			p, _ := strconv.ParseFloat(f[1], 64)
			if p > 0 {
				c.CPULimit = q / p
			}
		}
	}
	info, err := m.run(ctx, "lxc-info", "-n", name, "-H", "-s", "-p", "-i", "-S")
	if err == nil {
		for _, line := range strings.Split(info, "\n") {
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			switch strings.TrimSpace(k) {
			case "State":
				c.State = strings.ToLower(v)
			case "PID":
				c.PID, _ = strconv.Atoi(v)
			case "IP":
				c.IPs = append(c.IPs, v)
			case "Memory use":
				if n, err := strconv.ParseUint(v, 10, 64); err == nil {
					c.MemoryUsage = &n
				}
			}
		}
	}
	return c, nil
}

// Create builds an unprivileged container from the LXC image server using
// the download template (images are GPG-verified by the template).
func (m *Manager) Create(ctx context.Context, c agentapi.ContainerCreate, log func(string)) (*agentapi.Container, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if m.exists(c.Name) {
		return nil, fmt.Errorf("%w: a container named %q already exists", ErrConflict, c.Name)
	}
	if _, err := os.Stat(filepath.Join("/sys/class/net", c.Bridge, "bridge")); err != nil && m.Path == "/var/lib/lxc" {
		return nil, fmt.Errorf("%w: bridge %q does not exist", ErrConflict, c.Bridge)
	}

	base := m.baseConfig(c)
	tmp, err := os.CreateTemp("", "hm-lxc-*.conf")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(base); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()

	log(fmt.Sprintf("Downloading image %s/%s/%s and creating unprivileged container", c.Distribution, c.Release, c.Arch))
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	out, err := m.run(cctx, "lxc-create", "-n", c.Name, "-f", tmp.Name(), "-t", "download", "--",
		"--dist", c.Distribution, "--release", c.Release, "--arch", c.Arch)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			log(line)
		}
	}
	if err != nil {
		if m.exists(c.Name) {
			_, _ = m.run(context.Background(), "lxc-destroy", "-n", c.Name)
		}
		return nil, err
	}

	if err := m.appendConfig(c); err != nil {
		_, _ = m.run(context.Background(), "lxc-destroy", "-n", c.Name)
		return nil, err
	}
	_ = m.writeSidecar(c.Name, sidecar{Description: c.Description, Distribution: c.Distribution, Release: c.Release})
	if c.Start {
		log("Starting container")
		if err := m.Action(ctx, c.Name, "start"); err != nil {
			return nil, fmt.Errorf("container created, but start failed: %w", err)
		}
	}
	return m.Get(ctx, c.Name)
}

func (m *Manager) baseConfig(c agentapi.ContainerCreate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "lxc.idmap = u 0 %d %d\n", m.IDMap.UIDStart, m.IDMap.UIDCount)
	fmt.Fprintf(&b, "lxc.idmap = g 0 %d %d\n", m.IDMap.GIDStart, m.IDMap.GIDCount)
	b.WriteString("lxc.net.0.type = veth\n")
	fmt.Fprintf(&b, "lxc.net.0.link = %s\n", c.Bridge)
	b.WriteString("lxc.net.0.flags = up\n")
	b.WriteString("lxc.net.0.name = eth0\n")
	b.WriteString("lxc.apparmor.profile = generated\n")
	b.WriteString("lxc.apparmor.allow_nesting = 0\n")
	return b.String()
}

// limitLines renders resource limits for the detected cgroup version.
func (m *Manager) limitLines(memMiB, swapMiB uint64, cpu float64) []string {
	var lines []string
	if m.CGroup2 {
		if memMiB > 0 {
			lines = append(lines, fmt.Sprintf("lxc.cgroup2.memory.max = %d", memMiB<<20))
			lines = append(lines, fmt.Sprintf("lxc.cgroup2.memory.swap.max = %d", swapMiB<<20))
		}
		if cpu > 0 {
			lines = append(lines, fmt.Sprintf("lxc.cgroup2.cpu.max = %d 100000", int64(cpu*100000)))
		}
	} else {
		if memMiB > 0 {
			lines = append(lines, fmt.Sprintf("lxc.cgroup.memory.limit_in_bytes = %d", memMiB<<20))
		}
		if cpu > 0 {
			lines = append(lines, "lxc.cgroup.cpu.cfs_period_us = 100000", fmt.Sprintf("lxc.cgroup.cpu.cfs_quota_us = %d", int64(cpu*100000)))
		}
	}
	return lines
}

func (m *Manager) appendConfig(c agentapi.ContainerCreate) error {
	lines := []string{"", "# Host-managed"}
	lines = append(lines, "lxc.uts.name = "+c.Name)
	if c.Autostart {
		lines = append(lines, "lxc.start.auto = 1")
	}
	lines = append(lines, m.limitLines(c.MemoryMiB, c.SwapMiB, c.CPULimit)...)
	if c.IPv4 != "" {
		lines = append(lines, "lxc.net.0.ipv4.address = "+c.IPv4)
		if c.Gateway4 != "" {
			lines = append(lines, "lxc.net.0.ipv4.gateway = "+c.Gateway4)
		}
	}
	f, err := os.OpenFile(filepath.Join(m.dir(c.Name), "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.Join(lines, "\n") + "\n")
	return err
}

func (m *Manager) Action(ctx context.Context, name, action string) error {
	if !agentapi.ValidContainerAction(action) {
		return fmt.Errorf("%w: unknown action %q", ErrConflict, action)
	}
	if err := m.check(name); err != nil {
		return err
	}
	cur, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	running := cur.State == "running" || cur.State == "frozen"
	var args []string
	switch action {
	case "start":
		if running {
			return fmt.Errorf("%w: container is %s", ErrConflict, cur.State)
		}
		args = []string{"lxc-start", "-n", name}
	case "shutdown":
		if !running {
			return fmt.Errorf("%w: container is not running", ErrConflict)
		}
		args = []string{"lxc-stop", "-n", name, "-t", "60"}
	case "stop":
		if !running {
			return fmt.Errorf("%w: container is not running", ErrConflict)
		}
		args = []string{"lxc-stop", "-n", name, "-k"}
	case "reboot":
		if !running {
			return fmt.Errorf("%w: container is not running", ErrConflict)
		}
		args = []string{"lxc-stop", "-n", name, "-r"}
	case "freeze":
		args = []string{"lxc-freeze", "-n", name}
	case "unfreeze":
		args = []string{"lxc-unfreeze", "-n", name}
	}
	actx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	_, err = m.run(actx, args...)
	return err
}

func (m *Manager) Delete(ctx context.Context, name string) error {
	if err := m.check(name); err != nil {
		return err
	}
	cur, err := m.Get(ctx, name)
	if err != nil {
		return err
	}
	if cur.State != "stopped" {
		return fmt.Errorf("%w: container must be stopped before deletion", ErrConflict)
	}
	_, err = m.run(ctx, "lxc-destroy", "-n", name)
	return err
}

// SetLimits rewrites the resource limit lines and autostart flag. Limits
// take effect at the next start.
func (m *Manager) SetLimits(name string, memMiB, swapMiB uint64, cpu float64, autostart *bool, description *string) error {
	if err := m.check(name); err != nil {
		return err
	}
	if memMiB != 0 && memMiB < 32 || cpu < 0 || cpu > 512 {
		return fmt.Errorf("%w: invalid limits", ErrConflict)
	}
	path := filepath.Join(m.dir(name), "config")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		k, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		k = strings.TrimSpace(k)
		if strings.HasPrefix(k, "lxc.cgroup2.memory.") || k == "lxc.cgroup2.cpu.max" ||
			strings.HasPrefix(k, "lxc.cgroup.memory.") || strings.HasPrefix(k, "lxc.cgroup.cpu.") ||
			(autostart != nil && k == "lxc.start.auto") {
			continue
		}
		kept = append(kept, line)
	}
	kept = append(kept, m.limitLines(memMiB, swapMiB, cpu)...)
	if autostart != nil && *autostart {
		kept = append(kept, "lxc.start.auto = 1")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(kept, "\n")+"\n"), 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if description != nil {
		if len(*description) > 4096 {
			return fmt.Errorf("%w: description too long", ErrConflict)
		}
		sc := m.readSidecar(name)
		sc.Description = *description
		return m.writeSidecar(name, sc)
	}
	return nil
}

// ConsoleCommand returns the argument vector for an interactive root login
// inside a running container. Access control happens in the control plane.
func (m *Manager) ConsoleCommand(ctx context.Context, name string) ([]string, error) {
	c, err := m.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if c.State != "running" {
		return nil, fmt.Errorf("%w: container is not running", ErrConflict)
	}
	return []string{"lxc-attach", "-n", name, "--clear-env",
		"-v", "TERM=xterm-256color", "-v", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"--", "/bin/login", "-f", "root"}, nil
}

func (m *Manager) Version(ctx context.Context) string {
	out, err := m.run(ctx, "lxc-create", "--version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
