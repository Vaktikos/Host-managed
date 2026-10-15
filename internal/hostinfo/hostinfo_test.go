package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectFromFixture(t *testing.T) {
	root := t.TempDir()
	write(t, root, "proc/cpuinfo", `processor	: 0
model name	: Test CPU @ 3.00GHz
physical id	: 0
flags		: fpu vme svm sse
processor	: 1
model name	: Test CPU @ 3.00GHz
physical id	: 0
flags		: fpu vme svm sse
`)
	write(t, root, "proc/meminfo", "MemTotal:       16384 kB\nMemAvailable:    8192 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")
	write(t, root, "proc/loadavg", "0.50 0.25 0.10 1/100 1234\n")
	write(t, root, "proc/uptime", "3600.5 100.0\n")
	write(t, root, "proc/stat", "cpu  100 0 100 800 0 0 0 0 0 0\n")
	write(t, root, "etc/os-release", "PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\n")
	write(t, root, "sys/kernel/iommu_groups/0/x", "")
	old := Root
	Root = root
	defer func() { Root = old }()

	var s CPUSampler
	s.Sample()
	if s.Usage() != nil {
		t.Fatal("usage must be unknown after one sample")
	}
	write(t, root, "proc/stat", "cpu  150 0 150 900 0 0 0 0 0 0\n")
	s.Sample()
	if u := s.Usage(); u == nil || *u != 50 {
		t.Fatalf("usage = %v, want 50", u)
	}

	h := Collect(&s)
	if h.CPUThreads != 2 || h.CPUSockets != 1 || h.VirtExtensions != "svm" {
		t.Fatalf("cpu: %+v", h)
	}
	if h.MemTotal != 16384*1024 || h.MemAvailable != 8192*1024 {
		t.Fatalf("mem: %d %d", h.MemTotal, h.MemAvailable)
	}
	if h.Load[0] != 0.5 || h.UptimeSeconds != 3600 {
		t.Fatalf("load/uptime: %v %d", h.Load, h.UptimeSeconds)
	}
	if h.OS != "Debian GNU/Linux 13 (trixie)" || !h.IOMMUEnabled || h.KVMAvailable || h.BootMode != "bios" {
		t.Fatalf("misc: %+v", h)
	}
}

func TestInterfaceKinds(t *testing.T) {
	root := t.TempDir()
	write(t, root, "sys/class/net/eno1/device/x", "")
	write(t, root, "sys/class/net/eno1/address", "aa:bb:cc:dd:ee:ff\n")
	write(t, root, "sys/class/net/eno1/mtu", "1500\n")
	write(t, root, "sys/class/net/eno1/operstate", "up\n")
	write(t, root, "sys/class/net/eno1/speed", "1000\n")
	write(t, root, "sys/class/net/eno1/statistics/rx_bytes", "42\n")
	write(t, root, "sys/class/net/vmbr0/bridge/x", "")
	write(t, root, "sys/class/net/vmbr0/brif/eno1", "")
	write(t, root, "sys/class/net/eno1.10/uevent", "DEVTYPE=vlan\n")
	write(t, root, "sys/class/net/lo/mtu", "65536\n")
	old := Root
	Root = root
	defer func() { Root = old }()

	ifs, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, i := range ifs {
		got[i.Name] = i.Kind
		if i.Name == "eno1" && (i.Speed != 1000 || i.RxBytes != 42 || i.MTU != 1500) {
			t.Fatalf("eno1: %+v", i)
		}
		if i.Name == "vmbr0" && (len(i.Members) != 1 || i.Members[0] != "eno1") {
			t.Fatalf("vmbr0 members: %+v", i.Members)
		}
	}
	want := map[string]string{"eno1": "physical", "vmbr0": "bridge", "eno1.10": "vlan", "lo": "loopback"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: kind %q, want %q", k, got[k], v)
		}
	}
}
