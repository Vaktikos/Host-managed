package virt

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	libvirt "github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/cloudinit"
)

// newMAC returns a random address in the QEMU/KVM range 52:54:00.
func newMAC() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[0], b[1], b[2])
}

// firstMAC returns the MAC of the VM's first network card.
func firstMAC(x *libvirtxml.Domain) string {
	if x.Devices != nil {
		for _, ni := range x.Devices.Interfaces {
			if ni.MAC != nil {
				return ni.MAC.Address
			}
		}
	}
	return ""
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// seedDir picks a directory pool for the cloud-init seed: the disk pool if
// it is a directory pool, otherwise the pool named "iso".
func seedDir(l *libvirt.Libvirt, diskPool string) (string, libvirt.StoragePool, error) {
	for _, name := range []string{diskPool, "iso"} {
		p, err := l.StoragePoolLookupByName(name)
		if err != nil {
			continue
		}
		x, err := poolDef(l, p)
		if err == nil && x.Type == "dir" && x.Target != nil && x.Target.Path != "" {
			return x.Target.Path, p, nil
		}
	}
	return "", libvirt.StoragePool{}, fmt.Errorf("%w: cloud-init needs a directory storage pool for its seed image", ErrConflict)
}

// prepareCloudInit hashes a given password and validates the config.
func prepareCloudInit(ci *agentapi.CloudInit, previousHash string) error {
	if ci.Password == "" && ci.PasswordHash == "set" {
		ci.PasswordHash = previousHash
	}
	if ci.Password != "" {
		h, err := cloudinit.HashPassword(context.Background(), ci.Password)
		if err != nil {
			return err
		}
		ci.PasswordHash, ci.Password = h, ""
	}
	return ci.Validate()
}

// writeSeed renders and writes the seed ISO for the given VM UUID.
func writeSeed(path, uuid, mac string, ci agentapi.CloudInit) error {
	files, err := cloudinit.Files(uuid, mac, ci)
	if err != nil {
		return err
	}
	return cloudinit.WriteISO(context.Background(), files, path)
}

func metaCloudInit(meta hmMeta) (*agentapi.CloudInit, error) {
	if meta.CloudInit == "" {
		return nil, nil
	}
	var ci agentapi.CloudInit
	if err := json.Unmarshal([]byte(meta.CloudInit), &ci); err != nil {
		return nil, err
	}
	return &ci, nil
}

func setMetaCloudInit(meta *hmMeta, ci *agentapi.CloudInit, seed string) {
	if ci == nil {
		meta.CloudInit, meta.Seed = "", ""
		return
	}
	b, _ := json.Marshal(ci)
	meta.CloudInit, meta.Seed = string(b), seed
}

func seedDisk(path, target string) libvirtxml.DomainDisk {
	return libvirtxml.DomainDisk{
		Device:   "cdrom",
		Driver:   &libvirtxml.DomainDiskDriver{Name: "qemu", Type: "raw"},
		Source:   &libvirtxml.DomainDiskSource{File: &libvirtxml.DomainDiskSourceFile{File: path}},
		Target:   &libvirtxml.DomainDiskTarget{Dev: target, Bus: "sata"},
		ReadOnly: &libvirtxml.DomainDiskReadOnly{},
	}
}

// UpdateCloudInit replaces the cloud-init configuration and regenerates the
// seed. The guest applies it at its next boot (new instance ID).
func (m *Manager) UpdateCloudInit(name string, ci agentapi.CloudInit) error {
	l, dom, err := m.lookup(name)
	if err != nil {
		return err
	}
	x, _, err := m.domainXML(l, dom, true)
	if err != nil {
		return err
	}
	meta := parseMeta(x)
	old, err := metaCloudInit(meta)
	if err != nil || old == nil || meta.Seed == "" {
		return fmt.Errorf("%w: this VM was not created with cloud-init", ErrConflict)
	}
	if ci.Hostname == "" {
		ci.Hostname = name
	}
	if err := prepareCloudInit(&ci, old.PasswordHash); err != nil {
		return err
	}
	if err := writeSeed(meta.Seed, x.UUID, firstMAC(x), ci); err != nil {
		return err
	}
	setMetaCloudInit(&meta, &ci, meta.Seed)
	if err := m.writeMeta(l, dom, meta); err != nil {
		return err
	}
	// A running QEMU still holds the old file; re-insert the medium so the
	// next boot reads the new seed.
	if isRunning(l, dom) {
		for _, dk := range x.Devices.Disks {
			if dk.Device == "cdrom" && diskPath(dk) == meta.Seed {
				raw, _ := dk.Marshal()
				_ = l.DomainUpdateDeviceFlags(dom, raw, libvirt.DomainDeviceModifyLive)
			}
		}
	}
	_ = l.StoragePoolRefresh(mustPoolOfPath(l, meta.Seed), 0)
	return nil
}

func mustPoolOfPath(l *libvirt.Libvirt, path string) libvirt.StoragePool {
	if v, err := l.StorageVolLookupByPath(path); err == nil {
		if p, err := l.StoragePoolLookupByVolume(v); err == nil {
			return p
		}
	}
	return libvirt.StoragePool{}
}

func removeFile(path string) {
	if path != "" && filepath.IsAbs(path) {
		_ = os.Remove(path)
	}
}
