package virt

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	libvirt "github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

var poolStates = map[uint8]string{0: "inactive", 1: "building", 2: "running", 3: "degraded", 4: "inaccessible"}

// poolContent derives the intended content from the pool name convention
// used by the installer ("iso" holds installation media).
func poolContent(name string) string {
	if name == "iso" || strings.HasSuffix(name, "-iso") {
		return "iso"
	}
	return "images"
}

func (m *Manager) ListPools() ([]agentapi.StoragePool, error) {
	l, err := m.conn()
	if err != nil {
		return nil, err
	}
	pools, _, err := l.ConnectListAllStoragePools(1, 0)
	if err != nil {
		return nil, wrap(err)
	}
	out := make([]agentapi.StoragePool, 0, len(pools))
	for _, p := range pools {
		sp := agentapi.StoragePool{Name: p.Name, Content: poolContent(p.Name)}
		if st, capa, alloc, avail, err := l.StoragePoolGetInfo(p); err == nil {
			sp.State = poolStates[st]
			sp.Active = st == 2 || st == 3
			sp.Capacity, sp.Allocation, sp.Available = capa, alloc, avail
		}
		if a, err := l.StoragePoolGetAutostart(p); err == nil {
			sp.Autostart = a == 1
		}
		if x, err := poolDef(l, p); err == nil {
			sp.Type = x.Type
			if x.Target != nil {
				sp.Path = x.Target.Path
			}
		}
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

var volTypes = map[int8]string{0: "file", 1: "block", 2: "dir", 3: "network", 4: "netdir", 5: "ploop"}

func (m *Manager) ListVolumes(pool string) ([]agentapi.StorageVolume, error) {
	if !agentapi.ValidName(pool) {
		return nil, fmt.Errorf("%w: invalid pool name", ErrNotFound)
	}
	l, err := m.conn()
	if err != nil {
		return nil, err
	}
	p, err := l.StoragePoolLookupByName(pool)
	if err != nil {
		return nil, wrap(err)
	}
	// Pick up files that were added outside libvirt (e.g. an ISO copied in).
	_ = l.StoragePoolRefresh(p, 0)
	vols, _, err := l.StoragePoolListAllVolumes(p, 1, 0)
	if err != nil {
		return nil, wrap(err)
	}
	out := make([]agentapi.StorageVolume, 0, len(vols))
	for _, v := range vols {
		sv := agentapi.StorageVolume{Name: v.Name, Path: v.Key}
		if t, c, a, err := l.StorageVolGetInfo(v); err == nil {
			sv.Type, sv.Capacity, sv.Allocation = volTypes[t], c, a
		}
		if raw, err := l.StorageVolGetXMLDesc(v, 0); err == nil {
			var x libvirtxml.StorageVolume
			if x.Unmarshal(raw) == nil && x.Target != nil {
				sv.Path = x.Target.Path
				if x.Target.Format != nil {
					sv.Format = x.Target.Format.Type
				}
			}
		}
		out = append(out, sv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// UploadTarget resolves the file path for a new volume in a directory pool.
// The name is validated and the result is guaranteed to lie inside the pool.
func (m *Manager) UploadTarget(pool, name string) (string, error) {
	if !agentapi.ValidName(pool) || !agentapi.ValidVolumeName(name) {
		return "", fmt.Errorf("%w: invalid pool or file name", ErrConflict)
	}
	l, err := m.conn()
	if err != nil {
		return "", err
	}
	p, err := l.StoragePoolLookupByName(pool)
	if err != nil {
		return "", wrap(err)
	}
	x, err := poolDef(l, p)
	if err != nil {
		return "", err
	}
	if x.Type != "dir" || x.Target == nil || x.Target.Path == "" {
		return "", fmt.Errorf("%w: uploads are only supported into directory pools", ErrConflict)
	}
	dir := filepath.Clean(x.Target.Path)
	target := filepath.Join(dir, name)
	if filepath.Dir(target) != dir {
		return "", fmt.Errorf("%w: invalid file name", ErrConflict)
	}
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("%w: a file named %q already exists", ErrConflict, name)
	}
	return target, nil
}

// RefreshPool makes libvirt rescan a pool after an upload.
func (m *Manager) RefreshPool(pool string) error {
	l, err := m.conn()
	if err != nil {
		return err
	}
	p, err := l.StoragePoolLookupByName(pool)
	if err != nil {
		return wrap(err)
	}
	return wrap(l.StoragePoolRefresh(p, 0))
}

// DeleteVolume removes a volume unless a VM still references it.
func (m *Manager) DeleteVolume(pool, name string) error {
	if !agentapi.ValidName(pool) || !agentapi.ValidVolumeName(name) {
		return fmt.Errorf("%w: invalid pool or volume", ErrNotFound)
	}
	l, err := m.conn()
	if err != nil {
		return err
	}
	p, err := l.StoragePoolLookupByName(pool)
	if err != nil {
		return wrap(err)
	}
	v, err := l.StorageVolLookupByName(p, name)
	if err != nil {
		return wrap(err)
	}
	path, err := l.StorageVolGetPath(v)
	if err != nil {
		return wrap(err)
	}
	users, err := m.volumeUsers(l, path)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return fmt.Errorf("%w: volume is used by VM(s): %s", ErrConflict, strings.Join(users, ", "))
	}
	if b := m.backingUsers(l, path); len(b) > 0 {
		return fmt.Errorf("%w: volume is the base of linked clone volume(s): %s", ErrConflict, strings.Join(b, ", "))
	}
	return wrap(l.StorageVolDelete(v, 0))
}

func (m *Manager) volumeUsers(l *libvirt.Libvirt, path string) ([]string, error) {
	doms, _, err := l.ConnectListAllDomains(1, 0)
	if err != nil {
		return nil, wrap(err)
	}
	var users []string
	for _, d := range doms {
		x, _, err := m.domainXML(l, d, true)
		if err != nil || x.Devices == nil {
			continue
		}
		for _, dk := range x.Devices.Disks {
			if dk.Source != nil && dk.Source.File != nil && dk.Source.File.File == path {
				users = append(users, x.Name)
			}
		}
	}
	return users, nil
}

// ListNetworks returns libvirt virtual networks.
func (m *Manager) ListNetworks() ([]agentapi.VirtNetwork, error) {
	l, err := m.conn()
	if err != nil {
		return nil, err
	}
	nets, _, err := l.ConnectListAllNetworks(1, 0)
	if err != nil {
		return nil, wrap(err)
	}
	out := make([]agentapi.VirtNetwork, 0, len(nets))
	for _, n := range nets {
		vn := agentapi.VirtNetwork{Name: n.Name, UUID: uuidString(n.UUID), Forward: "isolated"}
		if a, err := l.NetworkIsActive(n); err == nil {
			vn.Active = a == 1
		}
		if a, err := l.NetworkGetAutostart(n); err == nil {
			vn.Autostart = a == 1
		}
		if raw, err := l.NetworkGetXMLDesc(n, 0); err == nil {
			var x libvirtxml.Network
			if x.Unmarshal(raw) == nil {
				if x.Bridge != nil {
					vn.Bridge = x.Bridge.Name
				}
				if x.Forward != nil {
					vn.Forward = x.Forward.Mode
					if vn.Forward == "" {
						vn.Forward = "nat"
					}
				}
				for _, ip := range x.IPs {
					if ip.Family == "" || ip.Family == "ipv4" {
						if ip.Prefix != 0 {
							vn.IPv4 = fmt.Sprintf("%s/%d", ip.Address, ip.Prefix)
						} else if ip.Netmask != "" {
							vn.IPv4 = ip.Address + "/" + ip.Netmask
						}
						break
					}
				}
			}
		}
		out = append(out, vn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
