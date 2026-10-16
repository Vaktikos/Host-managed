package cloudinit

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

func TestFilesAreJSONAndInjectionSafe(t *testing.T) {
	c := agentapi.CloudInit{
		User: "ops", Hostname: "web1.example.com", IPv4: "192.168.122.50/24", Gateway4: "192.168.122.1",
		IPv6: "auto", DNS: []string{"1.1.1.1"}, SSHKeys: []string{"ssh-ed25519 AAAAC3Nza key\nruncmd: [evil]"},
		GuestAgent: true, PasswordHash: "$6$abc$" + strings.Repeat("x", 86),
	}
	f, err := Files("uuid-1", "52:54:00:AA:BB:CC", c)
	if err != nil {
		t.Fatal(err)
	}
	ud := f["user-data"]
	if !strings.HasPrefix(string(ud), "#cloud-config\n") {
		t.Fatal("missing header")
	}
	var parsed map[string]any
	if err := json.Unmarshal(ud[len("#cloud-config\n"):], &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["runcmd"].([]any); !ok {
		t.Fatal("guest agent runcmd missing")
	}
	// The newline in the key stays inside a JSON string.
	if strings.Contains(string(ud), "\nruncmd: [evil]") {
		t.Fatal("YAML injection possible")
	}
	if parsed["fqdn"] != "web1.example.com" || parsed["hostname"] != "web1" {
		t.Fatalf("hostname: %v %v", parsed["hostname"], parsed["fqdn"])
	}
	var nc map[string]any
	json.Unmarshal(f["network-config"], &nc)
	eth := nc["ethernets"].(map[string]any)["eth0"].(map[string]any)
	if eth["match"].(map[string]any)["macaddress"] != "52:54:00:aa:bb:cc" {
		t.Fatalf("match: %v", eth["match"])
	}
	if eth["dhcp4"] != false || eth["addresses"].([]any)[0] != "192.168.122.50/24" || eth["accept-ra"] != true {
		t.Fatalf("network: %v", eth)
	}
	// Instance ID changes with the configuration.
	c2 := c
	c2.Hostname = "web2"
	f2, _ := Files("uuid-1", "52:54:00:AA:BB:CC", c2)
	if string(f2["meta-data"]) == string(f["meta-data"]) {
		t.Fatal("instance-id did not change")
	}
}

func TestValidation(t *testing.T) {
	bad := []agentapi.CloudInit{
		{User: "Root!", SSHKeys: []string{"ssh-ed25519 AAAA"}},
		{User: "ops"}, // no login method
		{User: "ops", SSHKeys: []string{"not a key"}},
		{User: "ops", Password: "short"},
		{User: "ops", Password: "longenough", IPv4: "10.0.0.1"},
		{User: "ops", Password: "longenough", Gateway4: "10.0.0.1"},
		{User: "ops", Password: "longenough", Packages: []string{"vim; rm -rf /"}},
		{User: "ops", Password: "longenough", Hostname: "bad_host"},
	}
	for i, c := range bad {
		if c.Validate() == nil {
			t.Errorf("case %d accepted: %+v", i, c)
		}
	}
	good := agentapi.CloudInit{User: "ops", SSHKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI comment"}, IPv4: "10.0.0.5/24", Gateway4: "10.0.0.1", Packages: []string{"htop"}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteISOAndHash(t *testing.T) {
	if _, err := exec.LookPath("genisoimage"); err != nil {
		t.Skip("genisoimage not installed")
	}
	h, err := HashPassword(context.Background(), "secret-password")
	if err != nil || !strings.HasPrefix(h, "$6$") {
		t.Fatalf("hash: %q %v", h, err)
	}
	c := agentapi.CloudInit{User: "ops", PasswordHash: h, Hostname: "x"}
	if err := c.Validate(); err != nil {
		t.Fatalf("generated hash rejected: %v", err)
	}
	f, _ := Files("u", "52:54:00:00:00:01", c)
	target := filepath.Join(t.TempDir(), "seed.iso")
	if err := WriteISO(context.Background(), f, target); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	if len(b) < 32768 || !strings.Contains(string(b[32768:34816]), "cidata") {
		t.Fatal("volume label cidata missing")
	}
}
