package lxc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

type call struct {
	name string
	args []string
}

func fakeManager(t *testing.T, onRun func(c call) (string, error)) (*Manager, *[]call) {
	t.Helper()
	var calls []call
	m := &Manager{Path: t.TempDir(), IDMap: IDMap{100000, 65536, 100000, 65536}, CGroup2: true}
	m.Run = func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, []byte, error) {
		c := call{name, args}
		calls = append(calls, c)
		out, err := onRun(c)
		return []byte(out), nil, err
	}
	return m, &calls
}

func TestCreateBuildsUnprivilegedConfig(t *testing.T) {
	var m *Manager
	m, calls := fakeManager(t, func(c call) (string, error) {
		switch c.name {
		case "lxc-create":
			// Emulate the template: copy the -f config into place.
			var cfg string
			for i, a := range c.args {
				if a == "-f" {
					b, _ := os.ReadFile(c.args[i+1])
					cfg = string(b)
				}
			}
			dir := filepath.Join(m.Path, "web1")
			os.MkdirAll(dir, 0o755)
			os.WriteFile(filepath.Join(dir, "config"), []byte(cfg), 0o644)
			return "Unpacking the rootfs\n", nil
		case "lxc-info":
			return "State: STOPPED\n", nil
		}
		return "", nil
	})
	ct, err := m.Create(context.Background(), agentapi.ContainerCreate{
		Name: "web1", Distribution: "debian", Release: "trixie", MemoryMiB: 512, SwapMiB: 0,
		CPULimit: 1.5, Bridge: "virbr0", IPv4: "192.168.122.50/24", Gateway4: "192.168.122.1", Autostart: true,
	}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	create := (*calls)[0]
	joined := strings.Join(create.args, " ")
	if !strings.Contains(joined, "-t download -- --dist debian --release trixie --arch amd64") {
		t.Fatalf("lxc-create args: %v", create.args)
	}
	cfg, _ := os.ReadFile(filepath.Join(m.Path, "web1", "config"))
	for _, want := range []string{
		"lxc.idmap = u 0 100000 65536", "lxc.net.0.link = virbr0",
		"lxc.cgroup2.memory.max = 536870912", "lxc.cgroup2.cpu.max = 150000 100000",
		"lxc.net.0.ipv4.address = 192.168.122.50/24", "lxc.start.auto = 1",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("config lacks %q:\n%s", want, cfg)
		}
	}
	if !ct.Unprivileged || !ct.Autostart || ct.MemoryLimit != 512<<20 || ct.CPULimit != 1.5 || ct.State != "stopped" {
		t.Fatalf("parsed container: %+v", ct)
	}
}

func TestRejectsInjection(t *testing.T) {
	m, calls := fakeManager(t, func(call) (string, error) { return "", nil })
	bad := []agentapi.ContainerCreate{
		{Name: "a;rm -rf /", Distribution: "debian", Release: "trixie", Bridge: "virbr0"},
		{Name: "ok", Distribution: "debian --keyserver evil", Release: "trixie", Bridge: "virbr0"},
		{Name: "ok", Distribution: "debian", Release: "trixie", Bridge: "br0\nlxc.mount.entry"},
		{Name: "ok", Distribution: "debian", Release: "trixie", Bridge: "virbr0", IPv4: "1.2.3.4/24\nlxc.x=1"},
	}
	for _, c := range bad {
		if _, err := m.Create(context.Background(), c, func(string) {}); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("commands were run for invalid input: %v", *calls)
	}
	if err := m.Action(context.Background(), "../etc", "start"); err == nil {
		t.Fatal("path traversal name accepted")
	}
}

func TestActionsRequireState(t *testing.T) {
	state := "STOPPED"
	m, calls := fakeManager(t, func(c call) (string, error) {
		if c.name == "lxc-info" {
			return "State: " + state + "\n", nil
		}
		return "", nil
	})
	os.MkdirAll(filepath.Join(m.Path, "c1"), 0o755)
	os.WriteFile(filepath.Join(m.Path, "c1", "config"), []byte("lxc.uts.name = c1\n"), 0o644)
	ctx := context.Background()
	if err := m.Action(ctx, "c1", "stop"); err == nil {
		t.Fatal("stop of stopped container accepted")
	}
	if err := m.Action(ctx, "c1", "start"); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	if last.name != "lxc-start" || strings.Join(last.args, " ") != "-n c1" {
		t.Fatalf("unexpected call %+v", last)
	}
	state = "RUNNING"
	if err := m.Delete(ctx, "c1"); err == nil {
		t.Fatal("delete of running container accepted")
	}
	if err := m.SetLimits("c1", 256, 0, 2, nil, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(m.Path, "c1", "config"))
	if !strings.Contains(string(cfg), "lxc.cgroup2.memory.max = 268435456") || !strings.Contains(string(cfg), "lxc.uts.name = c1") {
		t.Fatalf("limits not written: %s", cfg)
	}
}
