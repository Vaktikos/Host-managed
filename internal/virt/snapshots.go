package virt

import (
	"fmt"
	"sort"
	"strconv"

	libvirt "github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// Snapshots are libvirt internal snapshots stored inside qcow2 images. For a
// running VM they include the memory state. QEMU cannot take internal
// snapshots of running VMs with UEFI (pflash) firmware; those must be shut
// off first, which the error message says.

func usesPflash(x *libvirtxml.Domain) bool {
	return x.OS != nil && (x.OS.Firmware == "efi" || (x.OS.Loader != nil && x.OS.Loader.Type == "pflash"))
}

// checkSnapshotDisks verifies that every writable disk supports internal
// snapshots (qcow2).
func checkSnapshotDisks(x *libvirtxml.Domain) error {
	if x.Devices == nil {
		return nil
	}
	for _, dk := range x.Devices.Disks {
		if dk.Device != "disk" || dk.ReadOnly != nil {
			continue
		}
		if dk.Driver == nil || dk.Driver.Type != "qcow2" {
			t := "?"
			if dk.Target != nil {
				t = dk.Target.Dev
			}
			return fmt.Errorf("%w: disk %s is not qcow2; snapshots need qcow2 disks", ErrConflict, t)
		}
	}
	return nil
}

func (m *Manager) ListSnapshots(name string) ([]agentapi.Snapshot, error) {
	l, dom, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	snaps, _, err := l.DomainListAllSnapshots(dom, 1, 0)
	if err != nil {
		return nil, wrap(err)
	}
	cur := ""
	if c, err := l.DomainSnapshotCurrent(dom, 0); err == nil {
		cur = c.Name
	}
	out := make([]agentapi.Snapshot, 0, len(snaps))
	for _, s := range snaps {
		raw, err := l.DomainSnapshotGetXMLDesc(s, 0)
		if err != nil {
			continue
		}
		var x libvirtxml.DomainSnapshot
		if x.Unmarshal(raw) != nil {
			continue
		}
		sn := agentapi.Snapshot{Name: x.Name, Description: x.Description, State: x.State, Current: x.Name == cur}
		sn.CreatedAt, _ = strconv.ParseInt(x.CreationTime, 10, 64)
		if x.Parent != nil {
			sn.Parent = x.Parent.Name
		}
		if x.Memory != nil && x.Memory.Snapshot == "internal" {
			sn.Memory = true
		}
		out = append(out, sn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (m *Manager) CreateSnapshot(name string, c agentapi.SnapshotCreate) (*agentapi.Snapshot, error) {
	if err := c.Validate(); err != nil {
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
	if err := checkSnapshotDisks(x); err != nil {
		return nil, err
	}
	if isRunning(l, dom) && usesPflash(x) {
		return nil, fmt.Errorf("%w: snapshots of running UEFI VMs are not supported by QEMU; shut the VM off first", ErrConflict)
	}
	if _, err := l.DomainSnapshotLookupByName(dom, c.Name, 0); err == nil {
		return nil, &agentapi.FieldError{Field: "name", Reason: "a snapshot with this name already exists"}
	}
	raw, err := (&libvirtxml.DomainSnapshot{Name: c.Name, Description: c.Description}).Marshal()
	if err != nil {
		return nil, err
	}
	if _, err := l.DomainSnapshotCreateXML(dom, raw, uint32(libvirt.DomainSnapshotCreateAtomic)); err != nil {
		return nil, wrap(err)
	}
	list, err := m.ListSnapshots(name)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == c.Name {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("snapshot created but not listed")
}

// RevertSnapshot returns the VM to the snapshot state. Changes since then are
// lost; the UI confirms this.
func (m *Manager) RevertSnapshot(name, snap string) error {
	if !agentapi.ValidSnapshotName(snap) {
		return fmt.Errorf("%w: invalid snapshot", ErrNotFound)
	}
	l, dom, err := m.lookup(name)
	if err != nil {
		return err
	}
	s, err := l.DomainSnapshotLookupByName(dom, snap, 0)
	if err != nil {
		return wrap(err)
	}
	return wrap(l.DomainRevertToSnapshot(s, 0))
}

// DeleteSnapshot removes a snapshot; its data is merged into children.
func (m *Manager) DeleteSnapshot(name, snap string) error {
	if !agentapi.ValidSnapshotName(snap) {
		return fmt.Errorf("%w: invalid snapshot", ErrNotFound)
	}
	l, dom, err := m.lookup(name)
	if err != nil {
		return err
	}
	s, err := l.DomainSnapshotLookupByName(dom, snap, 0)
	if err != nil {
		return wrap(err)
	}
	return wrap(l.DomainSnapshotDelete(s, 0))
}
