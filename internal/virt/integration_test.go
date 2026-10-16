package virt

import (
	"net"
	"os"
	"testing"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// These tests run against a real libvirtd and create real domains. Enable
// with HM_LIBVIRT_INTEGRATION=1 on a disposable host.
func integration(t *testing.T) *Manager {
	t.Helper()
	if os.Getenv("HM_LIBVIRT_INTEGRATION") != "1" {
		t.Skip("set HM_LIBVIRT_INTEGRATION=1 to run against a real libvirtd")
	}
	m := New(Options{AllowTCG: true})
	t.Cleanup(m.Close)
	return m
}

func testPool(t *testing.T, m *Manager) string {
	t.Helper()
	return definePool(t, m, "hmtest-pool")
}

// testPool2 is a second directory pool holding a small fake ISO.
func testPool2(t *testing.T, m *Manager) string {
	t.Helper()
	name := definePool(t, m, "hmtest-iso")
	l, _ := m.conn()
	p, _ := l.StoragePoolLookupByName(name)
	x, _ := poolDef(l, p)
	if err := os.WriteFile(x.Target.Path+"/test.iso", make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = l.StoragePoolRefresh(p, 0)
	return name
}

func definePool(t *testing.T, m *Manager, name string) string {
	t.Helper()
	l, err := m.conn()
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir() is not reachable for the qemu user; use libvirt's tree.
	dir, err := os.MkdirTemp("/var/lib/libvirt/images", "hmtest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if old, err := l.StoragePoolLookupByName(name); err == nil {
		_ = l.StoragePoolDestroy(old)
		_ = l.StoragePoolUndefine(old)
	}
	xml := `<pool type='dir'><name>` + name + `</name><target><path>` + dir + `</path></target></pool>`
	p, err := l.StoragePoolDefineXML(xml, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.StoragePoolCreate(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = l.StoragePoolDestroy(p)
		_ = l.StoragePoolUndefine(p)
	})
	return name
}

func waitState(t *testing.T, m *Manager, name, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		vm, err := m.GetVM(name)
		if err != nil {
			t.Fatal(err)
		}
		if vm.State == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("VM %s did not reach state %s", name, want)
}

func TestIntegrationVMLifecycle(t *testing.T) {
	m := integration(t)
	pool := testPool(t, m)
	name := "hmtest-vm1"
	_, _ = m.DeleteVM(name, true) // leftovers from an aborted run
	if l, err := m.conn(); err == nil {
		if d, err := l.DomainLookupByName(name); err == nil {
			_ = l.DomainDestroy(d)
			_ = l.DomainUndefineFlags(d, libvirt.DomainUndefineNvram)
		}
	}

	var log []string
	spec := agentapi.VMCreate{
		Name: name, VCPUs: 1, MemoryMiB: 128, DiskPool: pool, DiskSizeGiB: 1,
		Network: "network:default", GuestAgent: true, Tags: []string{"test"},
		Description: "integration test", Start: true,
	}
	vm, err := m.CreateVM(spec, func(s string) { log = append(log, s) })
	if err != nil {
		t.Fatalf("create: %v (log: %v)", err, log)
	}
	t.Cleanup(func() {
		_ = m.VMAction(name, "stop")
		_, _ = m.DeleteVM(name, true)
	})
	if vm.State != "running" {
		t.Fatalf("state = %s, want running", vm.State)
	}
	if len(vm.Disks) != 2 || vm.Disks[0].Format != "qcow2" || vm.Disks[0].Capacity != 1<<30 {
		t.Fatalf("unexpected disks: %+v", vm.Disks)
	}
	if vm.Description != "integration test" || len(vm.Tags) != 1 {
		t.Fatalf("metadata not stored: %+v", vm.VM)
	}

	// A second create with the same name must be refused.
	if _, err := m.CreateVM(spec, func(string) {}); err == nil {
		t.Fatal("duplicate name accepted")
	}

	sock, err := m.VNCSocket(name)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial VNC socket: %v", err)
	}
	buf := make([]byte, 12)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(buf); err != nil || string(buf[:4]) != "RFB " {
		t.Fatalf("no RFB greeting: %q %v", buf, err)
	}
	c.Close()

	m.stats.sample()
	time.Sleep(1100 * time.Millisecond)
	m.stats.sample()
	st, err := m.VMStats(name)
	if err != nil || len(st) < 2 || st[len(st)-1].CPUUsage == nil {
		t.Fatalf("stats: %+v %v", st, err)
	}

	gi, err := m.GuestInfo(name)
	if err != nil || gi.Available {
		t.Fatalf("guest agent must report unavailable without a guest OS: %+v %v", gi, err)
	}

	if err := m.VMAction(name, "pause"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, name, "paused", 5*time.Second)
	if err := m.VMAction(name, "resume"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, name, "running", 5*time.Second)

	if err := m.VMAction(name, "start"); err == nil {
		t.Fatal("start of a running VM must fail")
	}
	if _, err := m.DeleteVM(name, true); err == nil {
		t.Fatal("deleting a running VM must fail")
	}

	pending, err := m.SetResources(name, agentapi.VMResources{VCPUs: 2, MemoryMiB: 256})
	if err != nil || !pending {
		t.Fatalf("resources: pending=%v err=%v", pending, err)
	}

	if err := m.VMAction(name, "stop"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, name, "shutoff", 10*time.Second)
	vm, _ = m.GetVM(name)
	if vm.VCPUs != 2 || vm.MemoryMiB != 256 {
		t.Fatalf("resources not applied: %d vCPU %d MiB", vm.VCPUs, vm.MemoryMiB)
	}

	desc, notes := "changed", "some notes"
	if err := m.SetMeta(name, agentapi.VMMeta{Description: &desc, Notes: &notes, Tags: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	vm, _ = m.GetVM(name)
	if vm.Description != desc || vm.Notes != notes || len(vm.Tags) != 2 {
		t.Fatalf("meta: %+v notes=%q", vm.VM, vm.Notes)
	}

	if err := m.SetTemplate(name, true); err != nil {
		t.Fatal(err)
	}
	if err := m.VMAction(name, "start"); err == nil {
		t.Fatal("template must not start")
	}
	if err := m.SetTemplate(name, false); err != nil {
		t.Fatal(err)
	}

	vols, _ := m.ListVolumes(pool)
	if len(vols) != 1 {
		t.Fatalf("volumes before delete: %+v", vols)
	}
	if err := m.DeleteVolume(pool, vols[0].Name); err == nil {
		t.Fatal("deleting a disk in use must fail")
	}
	removed, err := m.DeleteVM(name, true)
	if err != nil || len(removed) != 1 {
		t.Fatalf("delete: %v %v", removed, err)
	}
	vols, _ = m.ListVolumes(pool)
	if len(vols) != 0 {
		t.Fatalf("disk not removed: %+v", vols)
	}
	if _, err := m.GetVM(name); err == nil {
		t.Fatal("VM still exists")
	}
}

func TestIntegrationListings(t *testing.T) {
	m := integration(t)
	if _, err := m.ListVMs(); err != nil {
		t.Fatal(err)
	}
	nets, err := m.ListNetworks()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nets {
		if n.Name == "default" && n.Bridge != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("default network missing: %+v", nets)
	}
	lv, qv := m.Versions()
	if lv == "" || qv == "" {
		t.Fatalf("versions: %q %q", lv, qv)
	}
}
