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
