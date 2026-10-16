package agentapi

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Validation runs on both sides: in the control plane to give early errors
// and in the agent, which does not trust its caller.

var (
	nameRe    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)
	ctNameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	tagRe     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,31}$`)
	distRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	volNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,200}$`)
	ifNameRe  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,14}$`)
)

// FieldError names the offending field so the UI can show it inline.
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Reason }

func fieldErr(field, reason string, a ...any) error {
	return &FieldError{Field: field, Reason: fmt.Sprintf(reason, a...)}
}

func ValidName(s string) bool          { return nameRe.MatchString(s) }
func ValidContainerName(s string) bool { return ctNameRe.MatchString(s) }
func ValidVolumeName(s string) bool {
	return volNameRe.MatchString(s) && !strings.Contains(s, "..")
}
func ValidIfName(s string) bool { return ifNameRe.MatchString(s) }

func validTags(tags []string) error {
	if len(tags) > 16 {
		return fieldErr("tags", "too many tags (max 16)")
	}
	for _, t := range tags {
		if !tagRe.MatchString(t) {
			return fieldErr("tags", "invalid tag %q", t)
		}
	}
	return nil
}

// ParseNetworkRef splits "network:default" / "bridge:vmbr0".
func ParseNetworkRef(ref string) (kind, name string, err error) {
	kind, name, ok := strings.Cut(ref, ":")
	if !ok || (kind != "network" && kind != "bridge") || !ValidIfName(name) && !ValidName(name) {
		return "", "", fieldErr("network", "expected network:<name> or bridge:<name>")
	}
	return kind, name, nil
}

func (c *VMCreate) Validate() error {
	if !ValidName(c.Name) {
		return fieldErr("name", "1-63 characters: letters, digits, '.', '_', '-'")
	}
	if len(c.Description) > 4096 {
		return fieldErr("description", "too long")
	}
	if err := validTags(c.Tags); err != nil {
		return err
	}
	if c.VCPUs < 1 || c.VCPUs > 512 {
		return fieldErr("vcpus", "must be between 1 and 512")
	}
	if c.MemoryMiB < 64 || c.MemoryMiB > 16*1024*1024 {
		return fieldErr("memory_mib", "must be between 64 MiB and 16 TiB")
	}
	switch c.CPUMode {
	case "":
		c.CPUMode = "host-passthrough"
	case "host-passthrough", "host-model", "qemu64", "max":
	default:
		return fieldErr("cpu_mode", "unsupported CPU mode")
	}
	switch c.Firmware {
	case "":
		c.Firmware = "bios"
	case "bios", "uefi":
	default:
		return fieldErr("firmware", "must be bios or uefi")
	}
	if c.SecureBoot && c.Firmware != "uefi" {
		return fieldErr("secure_boot", "requires UEFI firmware")
	}
	if !ValidName(c.DiskPool) {
		return fieldErr("disk_pool", "invalid storage pool")
	}
	if c.DiskSizeGiB < 1 || c.DiskSizeGiB > 64*1024 {
		return fieldErr("disk_size_gib", "must be between 1 and 65536 GiB")
	}
	switch c.DiskBus {
	case "":
		c.DiskBus = "virtio"
	case "virtio", "sata", "scsi":
	default:
		return fieldErr("disk_bus", "must be virtio, sata or scsi")
	}
	if c.ISOVolume != "" || c.ISOPool != "" {
		if !ValidName(c.ISOPool) {
			return fieldErr("iso_pool", "invalid storage pool")
		}
		if !ValidVolumeName(c.ISOVolume) {
			return fieldErr("iso_volume", "invalid volume name")
		}
	}
	if c.ImageVolume != "" || c.ImagePool != "" {
		if !ValidName(c.ImagePool) {
			return fieldErr("image_pool", "invalid storage pool")
		}
		if !ValidVolumeName(c.ImageVolume) {
			return fieldErr("image_volume", "invalid volume name")
		}
	}
	if c.CloudInit != nil {
		if c.CloudInit.Hostname == "" {
			c.CloudInit.Hostname = c.Name
		}
		if err := c.CloudInit.Validate(); err != nil {
			return err
		}
	}
	if _, _, err := ParseNetworkRef(c.Network); err != nil {
		return err
	}
	switch c.NICModel {
	case "":
		c.NICModel = "virtio"
	case "virtio", "e1000", "rtl8139":
	default:
		return fieldErr("nic_model", "must be virtio, e1000 or rtl8139")
	}
	return nil
}

func (r *VMResources) Validate() error {
	if r.VCPUs < 1 || r.VCPUs > 512 {
		return fieldErr("vcpus", "must be between 1 and 512")
	}
	if r.MemoryMiB < 64 || r.MemoryMiB > 16*1024*1024 {
		return fieldErr("memory_mib", "must be between 64 MiB and 16 TiB")
	}
	return nil
}

func (m *VMMeta) Validate() error {
	if m.Description != nil && len(*m.Description) > 4096 {
		return fieldErr("description", "too long")
	}
	if m.Notes != nil && len(*m.Notes) > 65536 {
		return fieldErr("notes", "too long")
	}
	return validTags(m.Tags)
}

var vmActions = map[string]bool{
	"start": true, "shutdown": true, "stop": true, "reboot": true,
	"reset": true, "pause": true, "resume": true,
}

func ValidVMAction(a string) bool { return vmActions[a] }

var ctActions = map[string]bool{"start": true, "shutdown": true, "stop": true, "reboot": true, "freeze": true, "unfreeze": true}

func ValidContainerAction(a string) bool { return ctActions[a] }

func (c *ContainerCreate) Validate() error {
	if !ValidContainerName(c.Name) {
		return fieldErr("name", "1-63 characters: lowercase letters, digits, '-'")
	}
	if !distRe.MatchString(c.Distribution) {
		return fieldErr("distribution", "invalid distribution")
	}
	if !distRe.MatchString(c.Release) {
		return fieldErr("release", "invalid release")
	}
	switch c.Arch {
	case "":
		c.Arch = "amd64"
	case "amd64", "arm64", "i386":
	default:
		return fieldErr("arch", "unsupported architecture")
	}
	if c.MemoryMiB != 0 && c.MemoryMiB < 32 {
		return fieldErr("memory_mib", "at least 32 MiB or 0 for unlimited")
	}
	if c.CPULimit < 0 || c.CPULimit > 512 {
		return fieldErr("cpu_limit", "must be between 0 and 512")
	}
	if !ValidIfName(c.Bridge) {
		return fieldErr("bridge", "invalid bridge name")
	}
	if c.IPv4 != "" {
		p, err := netip.ParsePrefix(c.IPv4)
		if err != nil || !p.Addr().Is4() {
			return fieldErr("ipv4", "expected IPv4 CIDR, e.g. 192.168.122.50/24")
		}
		if c.Gateway4 != "" {
			g, err := netip.ParseAddr(c.Gateway4)
			if err != nil || !g.Is4() {
				return fieldErr("gateway4", "invalid IPv4 address")
			}
		}
	} else if c.Gateway4 != "" {
		return fieldErr("gateway4", "requires a static IPv4 address")
	}
	if len(c.Description) > 4096 {
		return fieldErr("description", "too long")
	}
	return nil
}

var snapNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

func ValidSnapshotName(s string) bool { return snapNameRe.MatchString(s) }

func validBus(b string) bool { return b == "virtio" || b == "sata" || b == "scsi" }

func (d *DiskAdd) Validate() error {
	if !ValidName(d.Pool) {
		return fieldErr("pool", "invalid storage pool")
	}
	if d.SizeGiB < 1 || d.SizeGiB > 64*1024 {
		return fieldErr("size_gib", "must be between 1 and 65536 GiB")
	}
	if d.Bus == "" {
		d.Bus = "virtio"
	}
	if !validBus(d.Bus) {
		return fieldErr("bus", "must be virtio, sata or scsi")
	}
	return nil
}

func (d *DiskResize) Validate() error {
	if d.SizeGiB < 1 || d.SizeGiB > 64*1024 {
		return fieldErr("size_gib", "must be between 1 and 65536 GiB")
	}
	return nil
}

func (m *Media) Validate() error {
	if m.Pool == "" && m.Volume == "" {
		return nil
	}
	if !ValidName(m.Pool) {
		return fieldErr("pool", "invalid storage pool")
	}
	if !ValidVolumeName(m.Volume) {
		return fieldErr("volume", "invalid volume name")
	}
	return nil
}

func (n *NICAdd) Validate() error {
	if _, _, err := ParseNetworkRef(n.Network); err != nil {
		return err
	}
	switch n.Model {
	case "":
		n.Model = "virtio"
	case "virtio", "e1000", "rtl8139":
	default:
		return fieldErr("model", "must be virtio, e1000 or rtl8139")
	}
	return nil
}

var macRe = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

func ValidMAC(s string) bool { return macRe.MatchString(s) }

var targetRe = regexp.MustCompile(`^(vd|sd|hd)[a-z]{1,2}$`)

func ValidDiskTarget(s string) bool { return targetRe.MatchString(s) }

func (b *BootOrder) Validate() error {
	if len(b.Order) == 0 || len(b.Order) > 16 {
		return fieldErr("order", "1 to 16 boot devices")
	}
	seen := map[string]bool{}
	for _, e := range b.Order {
		if seen[e] {
			return fieldErr("order", "duplicate entry %q", e)
		}
		seen[e] = true
		if mac, ok := strings.CutPrefix(e, "net:"); ok {
			if !ValidMAC(mac) {
				return fieldErr("order", "invalid NIC %q", e)
			}
		} else if !ValidDiskTarget(e) {
			return fieldErr("order", "invalid device %q", e)
		}
	}
	return nil
}

func (s *SnapshotCreate) Validate() error {
	if !ValidSnapshotName(s.Name) {
		return fieldErr("name", "1-63 characters: letters, digits, '.', '_', '-'")
	}
	if len(s.Description) > 1024 {
		return fieldErr("description", "too long")
	}
	return nil
}

func (c *Clone) Validate() error {
	if !ValidName(c.Name) {
		return fieldErr("name", "1-63 characters: letters, digits, '.', '_', '-'")
	}
	switch c.Mode {
	case "":
		c.Mode = "full"
	case "full", "linked":
	default:
		return fieldErr("mode", "must be full or linked")
	}
	if c.Pool != "" && !ValidName(c.Pool) {
		return fieldErr("pool", "invalid storage pool")
	}
	return nil
}

var (
	userRe    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	hostRe    = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)
	sshKeyRe  = regexp.MustCompile(`^(ssh-(rsa|ed25519|dss)|ecdsa-sha2-nistp(256|384|521)|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com) [A-Za-z0-9+/=]+( [^\r\n]{0,200})?$`)
	packageRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,63}$`)
	cryptRe   = regexp.MustCompile(`^\$6\$[./A-Za-z0-9]{1,16}\$[./A-Za-z0-9]{86}$`)
)

func (c *CloudInit) Validate() error {
	if c.User == "" {
		c.User = "admin"
	}
	if !userRe.MatchString(c.User) {
		return fieldErr("cloud_init.user", "lowercase letters, digits, '_', '-'; up to 32 characters")
	}
	if c.Password != "" && (len(c.Password) < 8 || len(c.Password) > 256 || strings.ContainsAny(c.Password, "\x00\n\r")) {
		return fieldErr("cloud_init.password", "8-256 characters")
	}
	if c.PasswordHash != "" && !cryptRe.MatchString(c.PasswordHash) {
		return fieldErr("cloud_init.password", "invalid password hash")
	}
	if len(c.SSHKeys) > 32 {
		return fieldErr("cloud_init.ssh_keys", "at most 32 keys")
	}
	for i, k := range c.SSHKeys {
		c.SSHKeys[i] = strings.TrimSpace(k)
		if !sshKeyRe.MatchString(c.SSHKeys[i]) {
			return fieldErr("cloud_init.ssh_keys", "key %d is not a valid OpenSSH public key", i+1)
		}
	}
	if c.Password == "" && c.PasswordHash == "" && len(c.SSHKeys) == 0 {
		return fieldErr("cloud_init.ssh_keys", "set an SSH key or a password, otherwise nobody can log in")
	}
	if c.Hostname != "" && (len(c.Hostname) > 253 || !hostRe.MatchString(c.Hostname)) {
		return fieldErr("cloud_init.hostname", "invalid host name")
	}
	if c.IPv4 != "" {
		p, err := netip.ParsePrefix(c.IPv4)
		if err != nil || !p.Addr().Is4() {
			return fieldErr("cloud_init.ipv4", "expected IPv4 CIDR, e.g. 192.168.122.50/24")
		}
		if c.Gateway4 != "" {
			g, err := netip.ParseAddr(c.Gateway4)
			if err != nil || !g.Is4() {
				return fieldErr("cloud_init.gateway4", "invalid IPv4 address")
			}
		}
	} else if c.Gateway4 != "" {
		return fieldErr("cloud_init.gateway4", "requires a static IPv4 address")
	}
	if c.IPv6 != "" && c.IPv6 != "auto" {
		p, err := netip.ParsePrefix(c.IPv6)
		if err != nil || !p.Addr().Is6() {
			return fieldErr("cloud_init.ipv6", "expected IPv6 CIDR or auto")
		}
		if c.Gateway6 != "" {
			g, err := netip.ParseAddr(c.Gateway6)
			if err != nil || !g.Is6() {
				return fieldErr("cloud_init.gateway6", "invalid IPv6 address")
			}
		}
	} else if c.Gateway6 != "" {
		return fieldErr("cloud_init.gateway6", "requires a static IPv6 address")
	}
	if len(c.DNS) > 4 {
		return fieldErr("cloud_init.dns", "at most 4 DNS servers")
	}
	for _, d := range c.DNS {
		if _, err := netip.ParseAddr(d); err != nil {
			return fieldErr("cloud_init.dns", "invalid address %q", d)
		}
	}
	if len(c.Packages) > 64 {
		return fieldErr("cloud_init.packages", "at most 64 packages")
	}
	for _, p := range c.Packages {
		if !packageRe.MatchString(p) {
			return fieldErr("cloud_init.packages", "invalid package name %q", p)
		}
	}
	return nil
}
