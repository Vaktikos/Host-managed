// Package cloudinit builds NoCloud seed images (ISO 9660, volume label
// "cidata") for cloud images. All documents are written as JSON, which is
// valid YAML, so user input can never inject additional YAML keys.
package cloudinit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// Files returns meta-data, user-data and network-config for a VM. The
// instance ID changes whenever the configuration changes, so cloud-init
// applies a new configuration on the next boot.
//
// mac identifies the first network card; the configuration matches it, since
// interface names differ between images (ens3, enp1s0, eth0 …).
func Files(vmUUID, mac string, c agentapi.CloudInit) (map[string][]byte, error) {
	cfg, _ := json.Marshal(c)
	sum := sha256.Sum256(append([]byte(vmUUID), cfg...))
	instance := "hm-" + vmUUID + "-" + hex.EncodeToString(sum[:4])

	meta, err := json.Marshal(map[string]string{"instance-id": instance, "local-hostname": c.Hostname})
	if err != nil {
		return nil, err
	}

	user := map[string]any{
		"name":        c.User,
		"groups":      []string{"sudo"},
		"shell":       "/bin/bash",
		"sudo":        "ALL=(ALL) NOPASSWD:ALL",
		"lock_passwd": c.PasswordHash == "",
	}
	if c.PasswordHash != "" {
		user["passwd"] = c.PasswordHash
	}
	if len(c.SSHKeys) > 0 {
		user["ssh_authorized_keys"] = c.SSHKeys
	}
	ud := map[string]any{
		"hostname":         strings.Split(c.Hostname, ".")[0],
		"preserve_hostname": false,
		"users":            []any{user},
		"ssh_pwauth":       c.PasswordHash != "",
		"disable_root":     true,
		"growpart":         map[string]any{"mode": "auto", "devices": []string{"/"}},
		"resize_rootfs":    true,
	}
	if strings.Contains(c.Hostname, ".") {
		ud["fqdn"] = c.Hostname
	}
	pkgs := append([]string(nil), c.Packages...)
	if c.GuestAgent {
		pkgs = append(pkgs, "qemu-guest-agent")
		ud["runcmd"] = []any{[]string{"systemctl", "enable", "--now", "qemu-guest-agent"}}
	}
	if len(pkgs) > 0 {
		ud["packages"] = pkgs
		ud["package_update"] = true
	}
	udJSON, err := json.Marshal(ud)
	if err != nil {
		return nil, err
	}

	eth := map[string]any{
		"match":    map[string]string{"macaddress": strings.ToLower(mac)},
		"set-name": "eth0",
	}
	var addrs []string
	if c.IPv4 != "" {
		addrs = append(addrs, c.IPv4)
		eth["dhcp4"] = false
	} else {
		eth["dhcp4"] = true
	}
	var routes []map[string]string
	if c.Gateway4 != "" {
		routes = append(routes, map[string]string{"to": "0.0.0.0/0", "via": c.Gateway4})
	}
	switch c.IPv6 {
	case "":
		eth["dhcp6"] = false
		eth["accept-ra"] = false
	case "auto":
		eth["dhcp6"] = true
		eth["accept-ra"] = true
	default:
		addrs = append(addrs, c.IPv6)
		eth["dhcp6"] = false
		if c.Gateway6 != "" {
			routes = append(routes, map[string]string{"to": "::/0", "via": c.Gateway6})
		}
	}
	if len(addrs) > 0 {
		eth["addresses"] = addrs
	}
	if len(routes) > 0 {
		eth["routes"] = routes
	}
	if len(c.DNS) > 0 {
		eth["nameservers"] = map[string]any{"addresses": c.DNS}
	}
	netJSON, err := json.Marshal(map[string]any{"version": 2, "ethernets": map[string]any{"eth0": eth}})
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"meta-data":      meta,
		"user-data":      append([]byte("#cloud-config\n"), udJSON...),
		"network-config": netJSON,
	}, nil
}

// HashPassword creates a SHA-512 crypt hash with openssl (fixed argv,
// password on stdin).
func HashPassword(ctx context.Context, pw string) (string, error) {
	cmd := exec.CommandContext(ctx, "openssl", "passwd", "-6", "-stdin")
	cmd.Stdin = strings.NewReader(pw + "\n")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("openssl passwd: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// WriteISO writes the seed image to target (replacing it atomically).
func WriteISO(ctx context.Context, files map[string][]byte, target string) error {
	dir, err := os.MkdirTemp("", "hm-cidata-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	names := []string{"meta-data", "user-data", "network-config"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), files[n], 0o600); err != nil {
			return err
		}
	}
	tmp := target + ".tmp"
	args := []string{"-quiet", "-output", tmp, "-volid", "cidata", "-joliet", "-rock"}
	for _, n := range names {
		args = append(args, filepath.Join(dir, n))
	}
	cmd := exec.CommandContext(ctx, "genisoimage", args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("genisoimage: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	// Readable for QEMU only through libvirt's dynamic ownership.
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, target)
}

// Public strips secrets for display.
func Public(c *agentapi.CloudInit) *agentapi.CloudInit {
	if c == nil {
		return nil
	}
	p := *c
	p.Password = ""
	if p.PasswordHash != "" {
		p.PasswordHash = "set"
	}
	return &p
}

// ValidIP is used by tests.
func ValidIP(s string) bool { _, err := netip.ParseAddr(s); return err == nil }
