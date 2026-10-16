package virt

import (
	"fmt"
	"strings"

	libvirt "github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// CloneVM copies a shut-off VM or template.
//
//   - full:   every disk is copied (qemu-img convert via libvirt); internal
//     snapshots are not copied.
//   - linked: every disk becomes a qcow2 overlay on top of the template's
//     disk. Only for templates (their disks must not change) and qcow2 disks
//     in directory pools. The template can no longer be deleted while
//     linked clones exist.
//
// The clone gets a new UUID, new MAC addresses, fresh UEFI variables and a
// fresh TPM state.
func (m *Manager) CloneVM(src string, c agentapi.Clone, log func(string)) (*agentapi.VMDetail, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	l, dom, err := m.lookup(src)
	if err != nil {
		return nil, err
	}
	if _, err := l.DomainLookupByName(c.Name); err == nil {
		return nil, &agentapi.FieldError{Field: "name", Reason: "a VM with this name already exists"}
	}
	if isRunning(l, dom) {
		return nil, fmt.Errorf("%w: the source VM must be shut off", ErrConflict)
	}
	x, _, err := m.domainXML(l, dom, true)
	if err != nil {
		return nil, err
	}
	meta := parseMeta(x)
	if c.Mode == "linked" && !meta.Template {
		return nil, fmt.Errorf("%w: linked clones can only be created from templates", ErrConflict)
	}

	var created []libvirt.StorageVol
	cleanup := func() {
		for _, v := range created {
			_ = l.StorageVolDelete(v, 0)
		}
		if len(created) > 0 {
			log(fmt.Sprintf("Cleanup: removed %d new volume(s)", len(created)))
		}
	}

	n := 0
	if x.Devices != nil {
		for i := range x.Devices.Disks {
			dk := &x.Devices.Disks[i]
			if dk.Device != "disk" {
				continue
			}
			path := diskPath(*dk)
			srcVol, err := l.StorageVolLookupByPath(path)
			if err != nil {
				cleanup()
				return nil, fmt.Errorf("%w: disk %s is not part of a storage pool and cannot be cloned", ErrConflict, path)
			}
			srcPool, err := l.StoragePoolLookupByVolume(srcVol)
			if err != nil {
				cleanup()
				return nil, wrap(err)
			}
			pool := srcPool
			if c.Pool != "" && c.Mode == "full" {
				if pool, err = l.StoragePoolLookupByName(c.Pool); err != nil {
					cleanup()
					return nil, wrap(err)
				}
			}
			format, ext, err := m.volumeFormat(l, pool)
			if err != nil {
				cleanup()
				return nil, err
			}
			_, capa, _, err := l.StorageVolGetInfo(srcVol)
			if err != nil {
				cleanup()
				return nil, wrap(err)
			}
			volName := fmt.Sprintf("%s-disk%d%s", c.Name, n, ext)
			n++
			vx := &libvirtxml.StorageVolume{
				Name:     volName,
				Capacity: &libvirtxml.StorageVolumeSize{Unit: "bytes", Value: capa},
				Target:   &libvirtxml.StorageVolumeTarget{Format: &libvirtxml.StorageVolumeTargetFormat{Type: format}},
			}
			var vol libvirt.StorageVol
			if c.Mode == "linked" {
				if format != "qcow2" || dk.Driver == nil || dk.Driver.Type != "qcow2" {
					cleanup()
					return nil, fmt.Errorf("%w: linked clones need qcow2 disks in a directory pool", ErrConflict)
				}
				vx.BackingStore = &libvirtxml.StorageVolumeBackingStore{
					Path:   path,
					Format: &libvirtxml.StorageVolumeTargetFormat{Type: "qcow2"},
				}
				raw, _ := vx.Marshal()
				log(fmt.Sprintf("Creating linked disk %s on top of %s", volName, path))
				vol, err = l.StorageVolCreateXML(pool, raw, 0)
			} else {
				raw, _ := vx.Marshal()
				log(fmt.Sprintf("Copying %s to %s (%d GiB)", path, volName, capa>>30))
				vol, err = l.StorageVolCreateXMLFrom(pool, raw, srcVol, 0)
			}
			if err != nil {
				cleanup()
				return nil, fmt.Errorf("clone disk: %w", wrap(err))
			}
			created = append(created, vol)
			newPath, err := l.StorageVolGetPath(vol)
			if err != nil {
				cleanup()
				return nil, wrap(err)
			}
			dk.Source = diskSource(newPath)
			dk.Driver.Type = format
			dk.BackingStore = nil
		}
		for i := range x.Devices.Interfaces {
			x.Devices.Interfaces[i].MAC = nil
			x.Devices.Interfaces[i].Target = nil
		}
		for i := range x.Devices.Graphics {
			if v := x.Devices.Graphics[i].VNC; v != nil {
				v.Socket = ""
				for j := range v.Listeners {
					if v.Listeners[j].Socket != nil {
						v.Listeners[j].Socket.Socket = ""
					}
				}
			}
		}
	}
	x.Name = c.Name
	x.UUID = ""
	if x.OS != nil {
		x.OS.NVRam = nil // libvirt creates fresh variables from the template
	}
	x.Metadata = nil
	raw, err := x.Marshal()
	if err != nil {
		cleanup()
		return nil, err
	}
	log("Defining " + c.Name)
	nd, err := l.DomainDefineXML(raw)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("define clone: %w", wrap(err))
	}
	meta.Template = false
	meta.Notes = strings.TrimSpace(meta.Notes)
	if meta.Description == "" {
		meta.Description = "Clone of " + src
	}
	if err := m.writeMeta(l, nd, meta); err != nil {
		log("Warning: metadata not copied: " + err.Error())
	}
	if c.Start {
		log("Starting " + c.Name)
		if err := l.DomainCreate(nd); err != nil {
			return nil, fmt.Errorf("clone created, but start failed: %w", wrap(err))
		}
	}
	return m.GetVM(c.Name)
}

// backingUsers lists volumes in any pool whose backing file is path.
func (m *Manager) backingUsers(l *libvirt.Libvirt, path string) []string {
	pools, _, err := l.ConnectListAllStoragePools(1, 0)
	if err != nil {
		return nil
	}
	var users []string
	for _, p := range pools {
		vols, _, err := l.StoragePoolListAllVolumes(p, 1, 0)
		if err != nil {
			continue
		}
		for _, v := range vols {
			raw, err := l.StorageVolGetXMLDesc(v, 0)
			if err != nil {
				continue
			}
			var vx libvirtxml.StorageVolume
			if vx.Unmarshal(raw) == nil && vx.BackingStore != nil && vx.BackingStore.Path == path {
				users = append(users, v.Name)
			}
		}
	}
	return users
}
