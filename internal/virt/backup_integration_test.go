package virt

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/backup"
)

func TestIntegrationBackupRestore(t *testing.T) {
	m := integration(t)
	pool := testPool(t, m)
	store, err := os.MkdirTemp("/var/lib/libvirt/images", "hmtest-backup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(store) })
	_ = os.Chmod(store, 0o755)
	names := []string{"hmtest-bk", "hmtest-bk-r1", "hmtest-bk-r2"}
	clean := func() {
		for _, n := range names {
			_ = m.VMAction(n, "stop")
			_, _ = m.DeleteVM(n, true)
		}
	}
	clean()
	t.Cleanup(clean)
	if _, err := m.CreateVM(agentapi.VMCreate{Name: "hmtest-bk", VCPUs: 1, MemoryMiB: 128, DiskPool: pool, DiskSizeGiB: 1,
		Network: "network:default", Start: true}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	var progressed bool
	rep := Reporter{Log: func(s string) { t.Log(s) }, Progress: func(d, tot int64) { progressed = progressed || tot > 0 }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Running VM: push backup.
	man, err := m.BackupVM(ctx, "hmtest-bk", store, "live", "test", rep)
	if err != nil {
		t.Fatal(err)
	}
	if man.Mode != "snapshot" || len(man.Disks) != 1 || man.Size == 0 || !progressed {
		t.Fatalf("live manifest: %+v progressed=%v", man, progressed)
	}
	// Offline backup.
	if err := m.VMAction("hmtest-bk", "stop"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "hmtest-bk", "shutoff", 30*time.Second)
	man2, err := m.BackupVM(ctx, "hmtest-bk", store, "", "test", rep)
	if err != nil {
		t.Fatal(err)
	}
	if man2.Mode != "offline" || man2.Consistency != "offline" {
		t.Fatalf("offline manifest: %+v", man2)
	}
	list, err := backup.List(store)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
	if n := backup.CleanPartial(store); len(n) != 0 {
		t.Fatalf("partial dirs left: %v", n)
	}

	// Restore under a new name while the original exists: new identity.
	orig, _ := m.GetVM("hmtest-bk")
	r1, err := m.RestoreVM(ctx, store, man.ID, agentapi.Restore{Name: "hmtest-bk-r1", Pool: pool, Start: true}, rep)
	if err != nil {
		t.Fatal(err)
	}
	if r1.UUID == orig.UUID || r1.State != "running" || len(r1.NICs) != 1 || r1.NICs[0].MAC == orig.NICs[0].MAC {
		t.Fatalf("restored: %+v", r1)
	}
	// The name is taken: refused.
	if _, err := m.RestoreVM(ctx, store, man2.ID, agentapi.Restore{Name: "hmtest-bk", Pool: pool}, rep); err == nil {
		t.Fatal("restore over an existing VM accepted")
	}
	// Disaster recovery: original gone, same name keeps UUID and MAC.
	if _, err := m.DeleteVM("hmtest-bk", true); err != nil {
		t.Fatal(err)
	}
	r2, err := m.RestoreVM(ctx, store, man2.ID, agentapi.Restore{Pool: pool}, rep)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Name != "hmtest-bk" || r2.UUID != orig.UUID || r2.NICs[0].MAC != orig.NICs[0].MAC {
		t.Fatalf("dr restore: %+v", r2)
	}
}
