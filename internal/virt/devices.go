package virt

import (
	"fmt"
	"strings"

	libvirt "github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// Device changes go to the persistent definition and, where QEMU supports
// hotplug (VirtIO/SCSI disks, NICs, CD-ROM media), also to the running VM.
// Everything else is reported as pending until the next start.

func isRunning(l *libvirt.Libvirt, d libvirt.Domain) bool {
	st, _, _ := l.DomainGetState(d, 0)
	s := stateName(st)
	return s == "running" || s == "paused" || s == "blocked"
}

func diskPath(dk libvirtxml.DomainDisk) string {
	if dk.Source == nil {
		return ""
	}
	switch {
	case dk.Source.File != nil:
		return dk.Source.File.File
	case dk.Source.Block != nil:
		return dk.Source.Block.Dev
	}
	return ""
}

// nextTarget returns the first free device name with the bus prefix.
func nextTarget(x *libvirtxml.Domain, bus string) string {
	prefix := "vd"
	if bus == "sata" || bus == "scsi" {
		prefix = "sd"
	}
	used := map[string]bool{}
	if x.Devices != nil {
		for _, dk := range x.Devices.Disks {
			if dk.Target != nil {
				used[dk.Target.Dev] = true
			}
		}
	}
	for c := 'a'; c <= 'z'; c++ {
		if t := prefix + string(c); !used[t] {
			return t
		}
	}
	return ""
}

func findDisk(x *libvirtxml.Domain, target string) *libvirtxml.DomainDisk {
	if x.Devices == nil {
		return nil
	}
	for i := range x.Devices.Disks {
		if x.Devices.Disks[i].Target != nil && x.Devices.Disks[i].Target.Dev == target {
			return &x.Devices.Disks[i]
		}
	}
	return nil
}

func (m *Manager) volumeFormat(l *libvirt.Libvirt, pool libvirt.StoragePool) (format, ext string, err error) {
	px, err := poolDef(l, pool)
	if err != nil {
		return "", "", err
	}
	switch px.Type {
	case "dir", "fs", "netfs":
		return "qcow2", ".qcow2", nil
	case "logical", "zfs", "rbd":
		return "raw", "", nil
	}
	return "", "", fmt.Errorf("%w: storage pool type %q is not supported for VM disks", ErrConflict, px.Type)
}

// freeVolumeName finds "<vm>-diskN<ext>" not yet used in the pool.
func freeVolumeName(l *libvirt.Libvirt, pool libvirt.StoragePool, vm, ext string) string {
	for i := 0; i < 100; i++ {
		n := fmt.Sprintf("%s-disk%d%s", vm, i, ext)
		if _, err := l.StorageVolLookupByName(pool, n); err != nil {
			return n
		}
	}
	return ""
}

func createVolume(l *libvirt.Libvirt, pool libvirt.StoragePool, name, format string, bytes uint64) (libvirt.StorageVol, error) {
	vx := &libvirtxml.StorageVolume{
		Name:     name,
		Capacity: &libvirtxml.StorageVolumeSize{Unit: "bytes", Value: bytes},
		Target:   &libvirtxml.StorageVolumeTarget{Format: &libvirtxml.StorageVolumeTargetFormat{Type: format}},
	}
	if format == "qcow2" {
		vx.Allocation = &libvirtxml.StorageVolumeSize{Unit: "bytes", Value: 0}
	}
	raw, err := vx.Marshal()
	if err != nil {
		return libvirt.StorageVol{}, err
	}
	v, err := l.StorageVolCreateXML(pool, raw, 0)
	return v, wrap(err)
}

// attach applies a device change live and persistently. If the running VM
// has no free hotplug slot, the change is stored persistently only and
// reported as pending.
func attach(l *libvirt.Libvirt, dom libvirt.Domain, raw string, live bool) (pending bool, err error) {
	err = l.DomainAttachDeviceFlags(dom, raw, attachFlags(live))
	if err != nil && live && strings.Contains(err.Error(), "No more available PCI slots") {
		return true, wrap(l.DomainAttachDeviceFlags(dom, raw, attachFlags(false)))
	}
	return false, wrap(err)
}

// detach removes a device from the persistent definition and, if live, from
// the running VM. Each side is matched with its own device description
// because addresses can differ between the two.
func (m *Manager) detach(l *libvirt.Libvirt, dom libvirt.Domain, live bool, pick func(x *libvirtxml.Domain) (string, bool)) error {
	if live {
		lx, _, err := m.domainXML(l, dom, false)
		if err != nil {
			return err
		}
		if raw, ok := pick(lx); ok {
			if err := l.DomainDetachDeviceFlags(dom, raw, uint32(libvirt.DomainDeviceModifyLive)); err != nil {
				return wrap(err)
			}
		}
	}
	cx, _, err := m.domainXML(l, dom, true)
	if err != nil {
		return err
	}
	if raw, ok := pick(cx); ok {
		return wrap(l.DomainDetachDeviceFlags(dom, raw, uint32(libvirt.DomainDeviceModifyConfig)))
	}
	return nil
}

func attachFlags(live bool) uint32 {
	f := uint32(libvirt.DomainDeviceModifyConfig)
	if live {
		f |= uint32(libvirt.DomainDeviceModifyLive)
	}
	return f
}

// AddDisk creates a volume and attaches it as a new disk.
func (m *Manager) AddDisk(name string, d agentapi.DiskAdd) (*agentapi.ChangeResult, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	l, dom, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	x, _, err := m.domainXML(l, dom, true)
	if err != nil {
		return nil, err
	}
	target := nextTarget(x, d.Bus)
	if target == "" {
		return nil, fmt.Errorf("%w: no free device name on bus %s", ErrConflict, d.Bus)
	}
	pool, err := l.StoragePoolLookupByName(d.Pool)
	if err != nil {
		return nil, wrap(err)
	}
	format, ext, err := m.volumeFormat(l, pool)
	if err != nil {
		return nil, err
	}
	volName := freeVolumeName(l, pool, name, ext)
	vol, err := createVolume(l, pool, volName, format, d.SizeGiB<<30)
	if err != nil {
		return nil, fmt.Errorf("create disk: %w", err)
	}
	path, err := l.StorageVolGetPath(vol)
	if err != nil {
		_ = l.StorageVolDelete(vol, 0)
		return nil, wrap(err)
	}
	running := isRunning(l, dom)
	// SATA (AHCI) does not support hotplug in QEMU.
	live := running && d.Bus != "sata"
	if d.Bus == "scsi" && !hasSCSIController(x) {
		ctl, _ := (&libvirtxml.DomainController{Type: "scsi", Model: "virtio-scsi"}).Marshal()
		if err := l.DomainAttachDeviceFlags(dom, ctl, attachFlags(live)); err != nil {
			_ = l.StorageVolDelete(vol, 0)
			return nil, wrap(err)
		}
	}
	disk := libvirtxml.DomainDisk{
		Device: "disk",
		Driver: &libvirtxml.DomainDiskDriver{Name: "qemu", Type: format, Discard: "unmap", Cache: "none"},
		Source: diskSource(path),
		Target: &libvirtxml.DomainDiskTarget{Dev: target, Bus: d.Bus},
	}
	raw, err := disk.Marshal()
	if err != nil {
		_ = l.StorageVolDelete(vol, 0)
		return nil, err
	}
	pending, err := attach(l, dom, raw, live)
	if err != nil {
		_ = l.StorageVolDelete(vol, 0)
		return nil, fmt.Errorf("attach disk: %w", err)
	}
	return &agentapi.ChangeResult{PendingRestart: (running && !live) || pending, Target: target}, nil
}

func hasSCSIController(x *libvirtxml.Domain) bool {
	if x.Devices == nil {
		return false
	}
	for _, c := range x.Devices.Controllers {
		if c.Type == "scsi" {
			return true
		}
	}
	return false
}

// RemoveDisk detaches a disk. The volume is deleted only on request and only
// while the VM is shut off, so a guest can never lose a disk it still writes.
func (m *Manager) RemoveDisk(name, target string, deleteVolume bool) (*agentapi.ChangeResult, error) {
	if !agentapi.ValidDiskTarget(target) {
		return nil, fmt.Errorf("%w: invalid disk", ErrNotFound)
	}
	l, dom, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	x, _, err := m.domainXML(l, dom, true)
	if err != nil {
		return nil, err
	}
	dk := findDisk(x, target)
	if dk == nil || dk.Device != "disk" {
		return nil, fmt.Errorf("%w: disk %s", ErrNotFound, target)
	}
	running := isRunning(l, dom)
	if deleteVolume && running {
		return nil, fmt.Errorf("%w: shut the VM off to delete the disk's data", ErrConflict)
	}
	path := diskPath(*dk)
	live := running && dk.Target.Bus != "sata"
	err = m.detach(l, dom, live, func(x *libvirtxml.Domain) (string, bool) {
		d := findDisk(x, target)
		if d == nil {
			return "", false
		}
		raw, err := d.Marshal()
		return raw, err == nil
	})
	if err != nil {
		return nil, err
	}
	if deleteVolume && path != "" {
		if users, _ := m.volumeUsers(l, path); len(users) > 0 {
			return nil, fmt.Errorf("disk detached, but the volume is still used by %s and was kept", strings.Join(users, ", "))
		}
		if v, err := l.StorageVolLookupByPath(path); err == nil {
			if err := l.StorageVolDelete(v, 0); err != nil {
				return nil, fmt.Errorf("disk detached, but deleting the volume failed: %w", wrap(err))
			}
		}
	}
	return &agentapi.ChangeResult{PendingRestart: running && !live}, nil
}

// ResizeDisk grows a disk; the guest still has to grow its partition.
func (m *Manager) ResizeDisk(name, target string, r agentapi.DiskResize) (*agentapi.ChangeResult, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if !agentapi.ValidDiskTarget(target) {
		return nil, fmt.Errorf("%w: invalid disk", ErrNotFound)
	}
	l, dom, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	x, _, err := m.domainXML(l, dom, true)
	if err != nil {
		return nil, err
	}
	dk := findDisk(x, target)
	if dk == nil || dk.Device != "disk" {
		return nil, fmt.Errorf("%w: disk %s", ErrNotFound, target)
	}
	vol, err := l.StorageVolLookupByPath(diskPath(*dk))
	if err != nil {
		return nil, fmt.Errorf("%w: disk is not a volume of a storage pool", ErrConflict)
	}
	_, capa, _, err := l.StorageVolGetInfo(vol)
	if err != nil {
		return nil, wrap(err)
	}
	want := r.SizeGiB << 30
	if want <= capa {
		return nil, fmt.Errorf("%w: new size must be larger than the current %d GiB; shrinking is not supported", ErrConflict, capa>>30)
	}
	if isRunning(l, dom) {
		err = l.DomainBlockResize(dom, target, want, libvirt.DomainBlockResizeBytes)
	} else {
		err = l.StorageVolResize(vol, want, 0)
	}
	if err != nil {
		return nil, wrap(err)
	}
	return &agentapi.ChangeResult{}, nil
}

// ChangeMedia inserts or ejects the medium of a CD-ROM drive.
func (m *Manager) ChangeMedia(name, target string, md agentapi.Media) error {
	if err := md.Validate(); err != nil {
		return err
	}
	l, dom, err := m.lookup(name)
	if err != nil {
		return err
	}
	x, _, err := m.domainXML(l, dom, true)
	if err != nil {
		return err
	}
	dk := findDisk(x, target)
	if dk == nil || dk.Device != "cdrom" {
		return fmt.Errorf("%w: CD-ROM drive %s", ErrNotFound, target)
	}
	cd := *dk
	cd.Source = nil
	if md.Volume != "" {
		p, err := l.StoragePoolLookupByName(md.Pool)
		if err != nil {
			return wrap(err)
		}
		v, err := l.StorageVolLookupByName(p, md.Volume)
		if err != nil {
			return wrap(err)
		}
		path, err := l.StorageVolGetPath(v)
		if err != nil {
			return wrap(err)
		}
		cd.Source = &libvirtxml.DomainDiskSource{File: &libvirtxml.DomainDiskSourceFile{File: path}}
	}
	raw, err := cd.Marshal()
	if err != nil {
		return err
	}
	return wrap(l.DomainUpdateDeviceFlags(dom, raw, libvirt.DomainDeviceModifyFlags(attachFlags(isRunning(l, dom)))))
}

// AddNIC attaches a network card (hotplug when running).
func (m *Manager) AddNIC(name string, n agentapi.NICAdd) (*agentapi.ChangeResult, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	l, dom, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	kind, src, _ := agentapi.ParseNetworkRef(n.Network)
	nic := libvirtxml.DomainInterface{Model: &libvirtxml.DomainInterfaceModel{Type: n.Model}}
	if kind == "network" {
		nic.Source = &libvirtxml.DomainInterfaceSource{Network: &libvirtxml.DomainInterfaceSourceNetwork{Network: src}}
	} else {
		nic.Source = &libvirtxml.DomainInterfaceSource{Bridge: &libvirtxml.DomainInterfaceSourceBridge{Bridge: src}}
	}
	raw, err := nic.Marshal()
	if err != nil {
		return nil, err
	}
	pending, err := attach(l, dom, raw, isRunning(l, dom))
	if err != nil {
		return nil, err
	}
	return &agentapi.ChangeResult{PendingRestart: pending}, nil
}

// RemoveNIC detaches the network card with the given MAC address.
func (m *Manager) RemoveNIC(name, mac string) (*agentapi.ChangeResult, error) {
	mac = strings.ToLower(mac)
	if !agentapi.ValidMAC(mac) {
		return nil, fmt.Errorf("%w: invalid MAC address", ErrNotFound)
	}
	l, dom, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	x, _, err := m.domainXML(l, dom, true)
	if err != nil {
		return nil, err
	}
	pick := func(x *libvirtxml.Domain) (string, bool) {
		if x.Devices == nil {
			return "", false
		}
		for _, ni := range x.Devices.Interfaces {
			if ni.MAC != nil && strings.ToLower(ni.MAC.Address) == mac {
				raw, err := ni.Marshal()
				return raw, err == nil
			}
		}
		return "", false
	}
	if _, ok := pick(x); !ok {
		return nil, fmt.Errorf("%w: network card %s", ErrNotFound, mac)
	}
	if err := m.detach(l, dom, isRunning(l, dom), pick); err != nil {
		return nil, err
	}
	return &agentapi.ChangeResult{}, nil
}

// SetBootOrder sets per-device boot order in the persistent definition.
func (m *Manager) SetBootOrder(name string, b agentapi.BootOrder) (*agentapi.ChangeResult, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	l, dom, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	x, _, err := m.domainXML(l, dom, true)
	if err != nil {
		return nil, err
	}
	pos := map[string]uint{}
	for i, e := range b.Order {
		pos[e] = uint(i + 1)
	}
	found := 0
	if x.OS != nil {
		x.OS.BootDevices = nil // per-device order replaces <os><boot dev=…>
	}
	if x.Devices != nil {
		for i := range x.Devices.Disks {
			dk := &x.Devices.Disks[i]
			dk.Boot = nil
			if dk.Target != nil {
				if o, ok := pos[dk.Target.Dev]; ok {
					dk.Boot = &libvirtxml.DomainDeviceBoot{Order: o}
					found++
				}
			}
		}
		for i := range x.Devices.Interfaces {
			ni := &x.Devices.Interfaces[i]
			ni.Boot = nil
			if ni.MAC != nil {
				if o, ok := pos["net:"+strings.ToLower(ni.MAC.Address)]; ok {
					ni.Boot = &libvirtxml.DomainDeviceBoot{Order: o}
					found++
				}
			}
		}
	}
	if found != len(b.Order) {
		return nil, &agentapi.FieldError{Field: "order", Reason: "contains devices the VM does not have"}
	}
	raw, err := x.Marshal()
	if err != nil {
		return nil, err
	}
	if _, err := l.DomainDefineXML(raw); err != nil {
		return nil, wrap(err)
	}
	return &agentapi.ChangeResult{PendingRestart: isRunning(l, dom)}, nil
}
