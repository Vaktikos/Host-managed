package virt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// Boots a real cloud image with a cloud-init seed and logs in over SSH.
// HM_CLOUD_IMAGE must point to a Debian/Ubuntu cloud image (qcow2).
func TestIntegrationCloudInit(t *testing.T) {
	m := integration(t)
	img := os.Getenv("HM_CLOUD_IMAGE")
	if img == "" {
		t.Skip("set HM_CLOUD_IMAGE to a cloud image to run")
	}
	pool := testPool(t, m)
	l, _ := m.conn()
	p, _ := l.StoragePoolLookupByName(pool)
	x, _ := poolDef(l, p)
	if out, err := exec.Command("cp", img, filepath.Join(x.Target.Path, "cloud.qcow2")).CombinedOutput(); err != nil {
		t.Fatalf("copy image: %v %s", err, out)
	}
	_ = l.StoragePoolRefresh(p, 0)

	key := filepath.Join(t.TempDir(), "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("keygen: %v %s", err, out)
	}
	pub, _ := os.ReadFile(key + ".pub")

	for _, n := range []string{"hmtest-ci", "hmtest-ci2"} {
		_ = m.VMAction(n, "stop")
		_, _ = m.DeleteVM(n, true)
	}
	t.Cleanup(func() {
		for _, n := range []string{"hmtest-ci", "hmtest-ci2"} {
			_ = m.VMAction(n, "stop")
			_, _ = m.DeleteVM(n, true)
		}
	})
	_, err := m.CreateVM(agentapi.VMCreate{
		Name: "hmtest-ci", VCPUs: 2, MemoryMiB: 1024, DiskPool: pool, DiskSizeGiB: 4,
		ImagePool: pool, ImageVolume: "cloud.qcow2", Network: "network:default", Start: true,
		CloudInit: &agentapi.CloudInit{User: "ops", Password: "test-password", SSHKeys: []string{strings.TrimSpace(string(pub))}, Hostname: "ci-test-host"},
	}, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	vm, _ := m.GetVM("hmtest-ci")
	if vm.CloudInit == nil || vm.CloudInit.PasswordHash != "set" || vm.CloudInit.Password != "" {
		t.Fatalf("cloud-init details leak or missing: %+v", vm.CloudInit)
	}
	if vm.Disks[0].Capacity != 4<<30 {
		t.Fatalf("disk not grown: %d", vm.Disks[0].Capacity)
	}
	mac := vm.NICs[0].MAC

	// Wait for a DHCP lease, then for SSH (software emulation is slow).
	var ip string
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) && ip == "" {
		out, _ := exec.Command("virsh", "-q", "net-dhcp-leases", "default", "--mac", mac).Output()
		if f := strings.Fields(string(out)); len(f) >= 5 {
			ip = strings.Split(f[4], "/")[0]
		}
		time.Sleep(5 * time.Second)
	}
	if ip == "" {
		t.Fatal("no DHCP lease")
	}
	var hostname string
	for time.Now().Before(deadline) {
		out, err := exec.Command("ssh", "-i", key, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
			"-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "ops@"+ip, "hostname; sudo -n true && echo sudo-ok").Output()
		if err == nil {
			hostname = string(out)
			break
		}
		time.Sleep(10 * time.Second)
	}
	if !strings.Contains(hostname, "ci-test-host") || !strings.Contains(hostname, "sudo-ok") {
		t.Fatalf("login result %q", hostname)
	}
	t.Logf("logged in via SSH: %q", hostname)

	// Clone: own seed with the new name.
	if err := m.VMAction("hmtest-ci", "stop"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "hmtest-ci", "shutoff", 30*time.Second)
	c, err := m.CloneVM("hmtest-ci", agentapi.Clone{Name: "hmtest-ci2", Mode: "full"}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if c.CloudInit == nil || c.CloudInit.Hostname != "hmtest-ci2" {
		t.Fatalf("clone cloud-init: %+v", c.CloudInit)
	}
	var seedOK bool
	for _, d := range c.Disks {
		if strings.HasSuffix(d.Source, "hmtest-ci2-cloudinit.iso") {
			seedOK = true
		}
	}
	if !seedOK {
		t.Fatalf("clone uses the source seed: %+v", c.Disks)
	}
	// Changing the configuration regenerates the seed.
	if err := m.UpdateCloudInit("hmtest-ci", agentapi.CloudInit{User: "ops", PasswordHash: "set", Hostname: "renamed"}); err != nil {
		t.Fatal(err)
	}
	vm, _ = m.GetVM("hmtest-ci")
	if vm.CloudInit.Hostname != "renamed" || vm.CloudInit.PasswordHash != "set" {
		t.Fatalf("update: %+v", vm.CloudInit)
	}
}
