package lxc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/backup"
)

func TestIntegrationContainerBackupRestore(t *testing.T) {
	if os.Getenv("HM_LXC_INTEGRATION") != "1" {
		t.Skip("set HM_LXC_INTEGRATION=1 to run against real LXC")
	}
	ctx := context.Background()
	imgs, err := ListImages(ctx, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	var alpine string
	for _, i := range imgs {
		if i.Distribution == "alpine" && i.Release != "edge" {
			alpine = i.Release
		}
	}
	m := New()
	names := []string{"hmtest-ctb", "hmtest-ctb2"}
	clean := func() {
		for _, n := range names {
			if m.exists(n) {
				_ = m.Action(ctx, n, "stop")
				_ = m.Delete(ctx, n)
			}
		}
	}
	clean()
	t.Cleanup(clean)
	if _, err := m.Create(ctx, agentapi.ContainerCreate{Name: "hmtest-ctb", Distribution: "alpine", Release: alpine,
		MemoryMiB: 128, Bridge: "virbr0", Start: true}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	// A marker owned by a shifted uid (container uid 1000).
	marker := filepath.Join(m.dir("hmtest-ctb"), "rootfs", "root", "hm-marker")
	if err := os.WriteFile(marker, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	uid := int(m.IDMap.UIDStart) + 1000
	if err := os.Chown(marker, uid, uid); err != nil {
		t.Fatal(err)
	}
	store := t.TempDir()
	rep := backup.Reporter{Log: func(s string) { t.Log(s) }}

	for _, mode := range []string{"suspend", "live", "stop"} {
		man, err := m.Backup(ctx, "hmtest-ctb", store, mode, "", "test", rep)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if man.Mode != mode || man.Size == 0 {
			t.Fatalf("%s manifest: %+v", mode, man)
		}
		ct, _ := m.Get(ctx, "hmtest-ctb")
		if ct.State != "running" {
			t.Fatalf("after %s backup the container is %s", mode, ct.State)
		}
		time.Sleep(1100 * time.Millisecond) // backup IDs have second resolution
	}
	list, _ := backup.List(store)
	if len(list) != 3 {
		t.Fatalf("list: %d", len(list))
	}
	if _, err := m.Restore(ctx, store, list[0].ID, agentapi.Restore{}, rep); err == nil {
		t.Fatal("restore over existing container accepted")
	}
	ct, err := m.Restore(ctx, store, list[0].ID, agentapi.Restore{Name: "hmtest-ctb2", Start: true}, rep)
	if err != nil {
		t.Fatal(err)
	}
	if ct.State != "running" || ct.Hostname != "hmtest-ctb2" {
		t.Fatalf("restored: %+v", ct)
	}
	cfg, _ := os.ReadFile(filepath.Join(m.dir("hmtest-ctb2"), "config"))
	if strings.Contains(string(cfg), "/hmtest-ctb/") {
		t.Fatalf("config still references the source:\n%s", cfg)
	}
	fi, err := os.Stat(filepath.Join(m.dir("hmtest-ctb2"), "rootfs", "root", "hm-marker"))
	if err != nil {
		t.Fatal(err)
	}
	if st := fi.Sys().(*syscall.Stat_t); int(st.Uid) != uid || fi.Mode().Perm() != 0o640 {
		t.Fatalf("ownership not kept: uid %d mode %v", st.Uid, fi.Mode())
	}
}
