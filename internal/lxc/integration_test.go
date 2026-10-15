package lxc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// Creates and starts a real unprivileged container. Requires root, LXC,
// network access to images.linuxcontainers.org and the virbr0 bridge.
func TestIntegrationContainerLifecycle(t *testing.T) {
	if os.Getenv("HM_LXC_INTEGRATION") != "1" {
		t.Skip("set HM_LXC_INTEGRATION=1 to run against real LXC")
	}
	ctx := context.Background()
	imgs, err := ListImages(ctx, "amd64")
	if err != nil || len(imgs) == 0 {
		t.Fatalf("images: %v", err)
	}
	var alpine string
	for _, i := range imgs {
		if i.Distribution == "alpine" && i.Release != "edge" {
			alpine = i.Release
		}
	}
	if alpine == "" {
		t.Fatal("no alpine image in index")
	}

	m := New()
	name := "hmtest-ct1"
	if m.exists(name) {
		_ = m.Action(ctx, name, "stop")
		_ = m.Delete(ctx, name)
	}
	var log []string
	ct, err := m.Create(ctx, agentapi.ContainerCreate{
		Name: name, Distribution: "alpine", Release: alpine, MemoryMiB: 256, CPULimit: 1,
		Bridge: "virbr0", Start: true, Description: "integration",
	}, func(s string) { log = append(log, s) })
	if err != nil {
		t.Fatalf("create: %v\n%s", err, strings.Join(log, "\n"))
	}
	t.Cleanup(func() {
		_ = m.Action(ctx, name, "stop")
		_ = m.Delete(ctx, name)
	})
	if ct.State != "running" || !ct.Unprivileged || ct.Description != "integration" {
		t.Fatalf("after create: %+v", ct)
	}
	if err := m.Action(ctx, name, "start"); err == nil {
		t.Fatal("double start accepted")
	}
	if err := m.Delete(ctx, name); err == nil {
		t.Fatal("delete of running container accepted")
	}
	if _, err := m.ConsoleCommand(ctx, name); err != nil {
		t.Fatal(err)
	}
	out, err := m.run(ctx, "lxc-attach", "-n", name, "--clear-env", "--", "/bin/cat", "/proc/self/uid_map")
	if err != nil || !strings.Contains(out, "100000") {
		t.Fatalf("container is not user-namespaced: %q %v", out, err)
	}
	if err := m.Action(ctx, name, "stop"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, _ := m.Get(ctx, name)
		if c.State == "stopped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %s", c.State)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := m.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	if m.exists(name) {
		t.Fatal("container still exists")
	}
}
