package virt

import (
	"strings"
	"testing"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

func TestIntegrationDevicesSnapshotsClones(t *testing.T) {
	m := integration(t)
	pool := testPool(t, m)
	isoPool := testPool2(t, m)
	names := []string{"hmtest-dev", "hmtest-full", "hmtest-linked"}
	for _, n := range names {
		_ = m.VMAction(n, "stop")
		_, _ = m.DeleteVM(n, true)
	}
	t.Cleanup(func() {
		for _, n := range []string{"hmtest-linked", "hmtest-full", "hmtest-dev"} {
			_ = m.VMAction(n, "stop")
			_, _ = m.DeleteVM(n, true)
		}
	})
	logf := func(string) {}
	_, err := m.CreateVM(agentapi.VMCreate{Name: "hmtest-dev", VCPUs: 1, MemoryMiB: 128, DiskPool: pool, DiskSizeGiB: 1,
		Network: "network:default", Start: true}, logf)
	if err != nil {
		t.Fatal(err)
	}

	// Hotplug a VirtIO disk into the running VM.
	res, err := m.AddDisk("hmtest-dev", agentapi.DiskAdd{Pool: pool, SizeGiB: 1, Bus: "virtio"})
	if err != nil || res.Target != "vdb" || res.PendingRestart {
		t.Fatalf("add disk: %+v %v", res, err)
	}
	// SATA cannot be hot-plugged: config only, pending restart.
	res, err = m.AddDisk("hmtest-dev", agentapi.DiskAdd{Pool: pool, SizeGiB: 1, Bus: "sata"})
	if err != nil || !res.PendingRestart {
		t.Fatalf("add sata: %+v %v", res, err)
	}
	// Online resize, then refuse to shrink.
	if _, err := m.ResizeDisk("hmtest-dev", "vdb", agentapi.DiskResize{SizeGiB: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResizeDisk("hmtest-dev", "vdb", agentapi.DiskResize{SizeGiB: 1}); err == nil {
		t.Fatal("shrink accepted")
	}
	// Deleting the volume of a running VM's disk is refused.
	if _, err := m.RemoveDisk("hmtest-dev", "vdb", true); err == nil {
		t.Fatal("volume deletion on a running VM accepted")
	}
	if _, err := m.RemoveDisk("hmtest-dev", "vdb", false); err != nil {
		t.Fatal(err)
	}
	// NIC hotplug and removal.
	if _, err := m.AddNIC("hmtest-dev", agentapi.NICAdd{Network: "network:default"}); err != nil {
		t.Fatal(err)
	}
	vm, _ := m.GetVM("hmtest-dev")
	if len(vm.NICs) != 2 {
		t.Fatalf("nics: %+v", vm.NICs)
	}
	if _, err := m.RemoveNIC("hmtest-dev", vm.NICs[1].MAC); err != nil {
		t.Fatal(err)
	}
	// Insert and eject a medium.
	if err := m.ChangeMedia("hmtest-dev", "sda", agentapi.Media{Pool: isoPool, Volume: "test.iso"}); err != nil {
		t.Fatal(err)
	}
	vm, _ = m.GetVM("hmtest-dev")
	if !strings.HasSuffix(findDiskDetail(vm, "sda").Source, "test.iso") {
		t.Fatalf("medium not inserted: %+v", vm.Disks)
	}
	if err := m.ChangeMedia("hmtest-dev", "sda", agentapi.Media{}); err != nil {
		t.Fatal(err)
	}
	if err := m.ChangeMedia("hmtest-dev", "vda", agentapi.Media{}); err == nil {
		t.Fatal("ejecting a hard disk accepted")
	}
	// Boot order.
	if _, err := m.SetBootOrder("hmtest-dev", agentapi.BootOrder{Order: []string{"sda", "vda"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetBootOrder("hmtest-dev", agentapi.BootOrder{Order: []string{"vdz"}}); err == nil {
		t.Fatal("unknown boot device accepted")
	}

	// Snapshot with memory while running (BIOS), revert, delete.
	if _, err := m.CreateSnapshot("hmtest-dev", agentapi.SnapshotCreate{Name: "before", Description: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSnapshot("hmtest-dev", agentapi.SnapshotCreate{Name: "before"}); err == nil {
		t.Fatal("duplicate snapshot accepted")
	}
	snaps, _ := m.ListSnapshots("hmtest-dev")
	if len(snaps) != 1 || !snaps[0].Memory || !snaps[0].Current || snaps[0].State != "running" {
		t.Fatalf("snapshots: %+v", snaps)
	}
	if err := m.VMAction("hmtest-dev", "stop"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "hmtest-dev", "shutoff", 10e9)
	if err := m.RevertSnapshot("hmtest-dev", "before"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "hmtest-dev", "running", 10e9) // memory snapshot resumes the VM
	if err := m.DeleteSnapshot("hmtest-dev", "before"); err != nil {
		t.Fatal(err)
	}
	if err := m.VMAction("hmtest-dev", "stop"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "hmtest-dev", "shutoff", 10e9)
	// Offline snapshot kept on purpose: the VM must still be deletable.
	if _, err := m.CreateSnapshot("hmtest-dev", agentapi.SnapshotCreate{Name: "offline"}); err != nil {
		t.Fatal(err)
	}

	// Full clone of a shut-off VM.
	full, err := m.CloneVM("hmtest-dev", agentapi.Clone{Name: "hmtest-full", Mode: "full"}, logf)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := m.GetVM("hmtest-dev")
	if full.UUID == src.UUID || full.NICs[0].MAC == src.NICs[0].MAC || full.Disks[0].Source == src.Disks[0].Source {
		t.Fatalf("clone shares identity with source: %+v", full)
	}
	if _, err := m.CloneVM("hmtest-dev", agentapi.Clone{Name: "hmtest-linked", Mode: "linked"}, logf); err == nil {
		t.Fatal("linked clone of a non-template accepted")
	}
	// Linked clone of a template.
	if err := m.SetTemplate("hmtest-dev", true); err != nil {
		t.Fatal(err)
	}
	linked, err := m.CloneVM("hmtest-dev", agentapi.Clone{Name: "hmtest-linked", Mode: "linked", Start: true}, logf)
	if err != nil {
		t.Fatal(err)
	}
	if linked.State != "running" || linked.Template {
		t.Fatalf("linked clone: %+v", linked.VM)
	}
	// The template's disks cannot be removed while the linked clone exists.
	if _, err := m.DeleteVM("hmtest-dev", true); err == nil || !strings.Contains(err.Error(), "linked clone") {
		t.Fatalf("template deletion with linked clones: %v", err)
	}
	if _, err := m.GetVM("hmtest-dev"); err != nil {
		t.Fatal("template was removed although deletion was refused")
	}
	_ = m.VMAction("hmtest-linked", "stop")
	waitState(t, m, "hmtest-linked", "shutoff", 10e9)
	if _, err := m.DeleteVM("hmtest-linked", true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteVM("hmtest-dev", true); err != nil {
		t.Fatalf("delete template with snapshot: %v", err)
	}
}

func findDiskDetail(vm *agentapi.VMDetail, target string) agentapi.VMDisk {
	for _, d := range vm.Disks {
		if d.Target == target {
			return d
		}
	}
	return agentapi.VMDisk{}
}
