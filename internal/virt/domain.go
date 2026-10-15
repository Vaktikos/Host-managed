package virt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"sort"
	"strings"

	libvirt "github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// metaNS is the namespace of the Host-managed element inside <metadata>.
// Description, tags and notes live in the domain definition itself, so
// libvirt stays the single source of truth for VM configuration.
const metaNS = "https://host-managed.dev/xmlns/vm/1"

type hmMeta struct {
	XMLName     xml.Name `xml:"vm"`
	Description string   `xml:"description,omitempty"`
	Notes       string   `xml:"notes,omitempty"`
	Tags        []string `xml:"tags>tag,omitempty"`
	Template    bool     `xml:"template,omitempty"`
}

func parseMeta(d *libvirtxml.Domain) hmMeta {
	var m hmMeta
	if d.Metadata == nil || d.Metadata.XML == "" {
		return m
	}
	dec := xml.NewDecoder(strings.NewReader(d.Metadata.XML))
	for {
		tok, err := dec.Token()
		if err != nil {
			return m
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Space == metaNS && se.Name.Local == "vm" {
			_ = dec.DecodeElement(&m, &se)
			return m
		}
	}
}

func stateName(s int32) string {
	switch libvirt.DomainState(s) {
	case libvirt.DomainRunning:
		return "running"
	case libvirt.DomainBlocked:
		return "blocked"
	case libvirt.DomainPaused:
		return "paused"
	case libvirt.DomainShutdown:
		return "shutting-down"
	case libvirt.DomainShutoff:
		return "shutoff"
	case libvirt.DomainCrashed:
		return "crashed"
	case libvirt.DomainPmsuspended:
		return "suspended"
	}
	return "unknown"
}

func (m *Manager) lookup(name string) (*libvirt.Libvirt, libvirt.Domain, error) {
	if !agentapi.ValidName(name) {
		return nil, libvirt.Domain{}, fmt.Errorf("%w: invalid VM name", ErrNotFound)
	}
	l, err := m.conn()
	if err != nil {
		return nil, libvirt.Domain{}, err
	}
	d, err := l.DomainLookupByName(name)
	if err != nil {
		return nil, libvirt.Domain{}, wrap(err)
	}
	return l, d, nil
}

func (m *Manager) domainXML(l *libvirt.Libvirt, d libvirt.Domain, inactive bool) (*libvirtxml.Domain, string, error) {
	var flags libvirt.DomainXMLFlags
	if inactive {
		flags = libvirt.DomainXMLInactive
	}
	raw, err := l.DomainGetXMLDesc(d, flags)
	if err != nil {
		return nil, "", wrap(err)
	}
	var x libvirtxml.Domain
	if err := x.Unmarshal(raw); err != nil {
		return nil, "", fmt.Errorf("parse domain XML: %w", err)
	}
	return &x, raw, nil
}

func memMiB(mem *libvirtxml.DomainMemory) uint64 {
	if mem == nil {
		return 0
	}
	v := uint64(mem.Value)
	switch strings.ToLower(mem.Unit) {
	case "b", "bytes":
		return v / (1 << 20)
	case "", "k", "kib":
		return v / 1024
	case "kb":
		return v * 1000 / (1 << 20)
	case "m", "mib":
		return v
	case "mb":
		return v * 1000 * 1000 / (1 << 20)
	case "g", "gib":
		return v * 1024
	case "gb":
		return v * 1000 * 1000 * 1000 / (1 << 20)
	case "t", "tib":
		return v * 1024 * 1024
	}
	return v / 1024
}

func summary(l *libvirt.Libvirt, d libvirt.Domain, x *libvirtxml.Domain) agentapi.VM {
	st, _, _ := l.DomainGetState(d, 0)
	auto, _ := l.DomainGetAutostart(d)
	meta := parseMeta(x)
	vcpus := 0
	if x.VCPU != nil {
		vcpus = int(x.VCPU.Value)
	}
	tags := meta.Tags
	if tags == nil {
		tags = []string{}
	}
	return agentapi.VM{
		UUID:        x.UUID,
		Name:        x.Name,
		State:       stateName(st),
		VCPUs:       vcpus,
		MemoryMiB:   memMiB(x.Memory),
		Autostart:   auto == 1,
		Description: meta.Description,
		Tags:        tags,
		Template:    meta.Template,
	}
}

// ListVMs returns all defined and transient domains sorted by name.
func (m *Manager) ListVMs() ([]agentapi.VM, error) {
	l, err := m.conn()
	if err != nil {
		return nil, err
	}
	doms, _, err := l.ConnectListAllDomains(1, 0)
	if err != nil {
		return nil, wrap(err)
	}
	out := make([]agentapi.VM, 0, len(doms))
	for _, d := range doms {
		x, _, err := m.domainXML(l, d, false)
		if err != nil {
			// The domain may have been undefined between the two calls.
			continue
		}
		out = append(out, summary(l, d, x))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) GetVM(name string) (*agentapi.VMDetail, error) {
	l, d, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	x, raw, err := m.domainXML(l, d, false)
	if err != nil {
		return nil, err
	}
	det := &agentapi.VMDetail{VM: summary(l, d, x), XML: raw}
	det.Notes = parseMeta(x).Notes
	det.DomainType = x.Type
	det.Firmware = "bios"
	if x.OS != nil {
		if x.OS.Type != nil {
			det.Machine = x.OS.Type.Machine
		}
		if x.OS.Firmware == "efi" || (x.OS.Loader != nil && x.OS.Loader.Type == "pflash") {
			det.Firmware = "uefi"
		}
		if x.OS.Loader != nil && x.OS.Loader.Secure == "yes" {
			det.SecureBoot = true
		}
		if x.OS.FirmwareInfo != nil {
			for _, f := range x.OS.FirmwareInfo.Features {
				if f.Name == "secure-boot" && f.Enabled == "yes" {
					det.SecureBoot = true
				}
			}
		}
	}
	if x.CPU != nil {
		det.CPUMode = x.CPU.Mode
	}
	if x.Devices != nil {
		type bootEntry struct {
			order int
			name  string
		}
		var boots []bootEntry
		for _, dk := range x.Devices.Disks {
			vd := agentapi.VMDisk{Device: dk.Device, ReadOnly: dk.ReadOnly != nil}
			if dk.Target != nil {
				vd.Target = dk.Target.Dev
				vd.Bus = dk.Target.Bus
			}
			if dk.Source != nil {
				switch {
				case dk.Source.File != nil:
					vd.Source = dk.Source.File.File
				case dk.Source.Block != nil:
					vd.Source = dk.Source.Block.Dev
				case dk.Source.Volume != nil:
					vd.Source = dk.Source.Volume.Pool + "/" + dk.Source.Volume.Volume
				}
			}
			if dk.Driver != nil {
				vd.Format = dk.Driver.Type
			}
			if vd.Source != "" && strings.HasPrefix(vd.Source, "/") {
				if vol, err := l.StorageVolLookupByPath(vd.Source); err == nil {
					if _, c, _, err := l.StorageVolGetInfo(vol); err == nil {
						vd.Capacity = c
					}
				}
			}
			if dk.Boot != nil {
				boots = append(boots, bootEntry{int(dk.Boot.Order), vd.Target})
			}
			det.Disks = append(det.Disks, vd)
		}
		for _, ni := range x.Devices.Interfaces {
			n := agentapi.VMNIC{}
			if ni.MAC != nil {
				n.MAC = ni.MAC.Address
			}
			if ni.Model != nil {
				n.Model = ni.Model.Type
			}
			if ni.Source != nil {
				switch {
				case ni.Source.Network != nil:
					n.Type, n.Source = "network", ni.Source.Network.Network
				case ni.Source.Bridge != nil:
					n.Type, n.Source = "bridge", ni.Source.Bridge.Bridge
				}
			}
			if ni.Target != nil {
				n.Target = ni.Target.Dev
			}
			if ni.Boot != nil {
				boots = append(boots, bootEntry{int(ni.Boot.Order), "net:" + n.MAC})
			}
			det.NICs = append(det.NICs, n)
		}
		sort.Slice(boots, func(i, j int) bool { return boots[i].order < boots[j].order })
		for _, b := range boots {
			det.BootOrder = append(det.BootOrder, b.name)
		}
		for _, g := range x.Devices.Graphics {
			if g.VNC != nil {
				det.Console.VNC = true
			}
		}
		for _, c := range x.Devices.Channels {
			if c.Target != nil && c.Target.VirtIO != nil && c.Target.VirtIO.Name == "org.qemu.guest_agent.0" {
				det.GuestAgent = true
			}
		}
		det.TPM = len(x.Devices.TPMs) > 0
	}
	if det.Disks == nil {
		det.Disks = []agentapi.VMDisk{}
	}
	if det.NICs == nil {
		det.NICs = []agentapi.VMNIC{}
	}
	return det, nil
}

// VMAction changes the power state.
func (m *Manager) VMAction(name, action string) error {
	if !agentapi.ValidVMAction(action) {
		return fmt.Errorf("%w: unknown action %q", ErrConflict, action)
	}
	l, d, err := m.lookup(name)
	if err != nil {
		return err
	}
	st, _, err := l.DomainGetState(d, 0)
	if err != nil {
		return wrap(err)
	}
	state := stateName(st)
	switch action {
	case "start":
		x, _, err := m.domainXML(l, d, true)
		if err != nil {
			return err
		}
		if parseMeta(x).Template {
			return fmt.Errorf("%w: VM is a template and cannot be started", ErrConflict)
		}
		if state != "shutoff" && state != "crashed" {
			return fmt.Errorf("%w: VM is %s", ErrConflict, state)
		}
		return wrap(l.DomainCreate(d))
	case "shutdown":
		// ACPI first; libvirt falls back to the guest agent if configured.
		return wrap(l.DomainShutdownFlags(d, 0))
	case "stop":
		if state == "shutoff" {
			return fmt.Errorf("%w: VM is not running", ErrConflict)
		}
		return wrap(l.DomainDestroy(d))
	case "reboot":
		return wrap(l.DomainReboot(d, 0))
	case "reset":
		return wrap(l.DomainReset(d, 0))
	case "pause":
		return wrap(l.DomainSuspend(d))
	case "resume":
		return wrap(l.DomainResume(d))
	}
	return nil
}

// SetMeta updates description, notes, tags and autostart.
func (m *Manager) SetMeta(name string, upd agentapi.VMMeta) error {
	if err := upd.Validate(); err != nil {
		return err
	}
	l, d, err := m.lookup(name)
	if err != nil {
		return err
	}
	x, _, err := m.domainXML(l, d, true)
	if err != nil {
		return err
	}
	meta := parseMeta(x)
	if upd.Description != nil {
		meta.Description = *upd.Description
	}
	if upd.Notes != nil {
		meta.Notes = *upd.Notes
	}
	if upd.Tags != nil {
		meta.Tags = upd.Tags
	}
	if err := m.writeMeta(l, d, meta); err != nil {
		return err
	}
	if upd.Autostart != nil {
		v := int32(0)
		if *upd.Autostart {
			v = 1
		}
		if err := l.DomainSetAutostart(d, v); err != nil {
			return wrap(err)
		}
	}
	return nil
}

func (m *Manager) writeMeta(l *libvirt.Libvirt, d libvirt.Domain, meta hmMeta) error {
	meta.XMLName = xml.Name{Local: "vm"}
	b, err := xml.Marshal(meta)
	if err != nil {
		return err
	}
	err = l.DomainSetMetadata(d, int32(libvirt.DomainMetadataElement), libvirt.OptString{string(b)},
		libvirt.OptString{"hm"}, libvirt.OptString{metaNS}, libvirt.DomainAffectConfig)
	if err != nil {
		return wrap(err)
	}
	if st, _, _ := l.DomainGetState(d, 0); stateName(st) != "shutoff" {
		// Keep the live definition in sync; failure here is harmless.
		_ = l.DomainSetMetadata(d, int32(libvirt.DomainMetadataElement), libvirt.OptString{string(b)},
			libvirt.OptString{"hm"}, libvirt.OptString{metaNS}, libvirt.DomainAffectLive)
	}
	return nil
}

// SetTemplate marks or unmarks a VM as template. Templates must be shut off.
func (m *Manager) SetTemplate(name string, template bool) error {
	l, d, err := m.lookup(name)
	if err != nil {
		return err
	}
	if st, _, _ := l.DomainGetState(d, 0); template && stateName(st) != "shutoff" {
		return fmt.Errorf("%w: VM must be shut off", ErrConflict)
	}
	x, _, err := m.domainXML(l, d, true)
	if err != nil {
		return err
	}
	meta := parseMeta(x)
	meta.Template = template
	if template {
		_ = l.DomainSetAutostart(d, 0)
	}
	return m.writeMeta(l, d, meta)
}

// SetResources updates vCPU count and memory in the persistent definition.
// It reports whether a restart is needed for the change to take effect.
func (m *Manager) SetResources(name string, r agentapi.VMResources) (pendingRestart bool, err error) {
	if err := r.Validate(); err != nil {
		return false, err
	}
	l, d, err := m.lookup(name)
	if err != nil {
		return false, err
	}
	x, _, err := m.domainXML(l, d, true)
	if err != nil {
		return false, err
	}
	if x.VCPU == nil {
		x.VCPU = &libvirtxml.DomainVCPU{Placement: "static"}
	}
	x.VCPU.Value = uint(r.VCPUs)
	x.VCPU.Current = 0
	x.Memory = &libvirtxml.DomainMemory{Value: uint(r.MemoryMiB), Unit: "MiB"}
	x.CurrentMemory = &libvirtxml.DomainCurrentMemory{Value: uint(r.MemoryMiB), Unit: "MiB"}
	if x.CPU != nil && x.CPU.Topology != nil {
		// A fixed topology would contradict the new count.
		x.CPU.Topology = nil
	}
	out, err := x.Marshal()
	if err != nil {
		return false, err
	}
	if _, err := l.DomainDefineXML(out); err != nil {
		return false, wrap(err)
	}
	st, _, _ := l.DomainGetState(d, 0)
	return stateName(st) != "shutoff", nil
}

// DeleteVM undefines a shut-off VM. With deleteDisks it also removes disk
// volumes that belong to a libvirt storage pool; CD-ROM media and disks
// outside any pool are never removed.
func (m *Manager) DeleteVM(name string, deleteDisks bool) (removed []string, err error) {
	l, d, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	if st, _, _ := l.DomainGetState(d, 0); stateName(st) != "shutoff" {
		return nil, fmt.Errorf("%w: VM must be shut off before deletion", ErrConflict)
	}
	x, _, err := m.domainXML(l, d, true)
	if err != nil {
		return nil, err
	}
	var vols []libvirt.StorageVol
	if deleteDisks && x.Devices != nil {
		for _, dk := range x.Devices.Disks {
			if dk.Device != "disk" || dk.Source == nil {
				continue
			}
			var path string
			switch {
			case dk.Source.File != nil:
				path = dk.Source.File.File
			case dk.Source.Block != nil:
				path = dk.Source.Block.Dev
			}
			if path == "" {
				continue
			}
			if v, err := l.StorageVolLookupByPath(path); err == nil {
				vols = append(vols, v)
				removed = append(removed, path)
			}
		}
	}
	flags := libvirt.DomainUndefineManagedSave | libvirt.DomainUndefineNvram | libvirt.DomainUndefineTpm
	if err := l.DomainUndefineFlags(d, flags); err != nil {
		var le libvirt.Error
		if errors.As(err, &le) && (le.Code == uint32(libvirt.ErrInvalidArg) || le.Code == uint32(libvirt.ErrArgumentUnsupported)) {
			err = l.DomainUndefineFlags(d, libvirt.DomainUndefineManagedSave|libvirt.DomainUndefineNvram)
		}
		if err != nil {
			return nil, wrap(err)
		}
	}
	var errs []string
	for i, v := range vols {
		if err := l.StorageVolDelete(v, 0); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", removed[i], err))
		}
	}
	if len(errs) > 0 {
		return removed, fmt.Errorf("VM removed, but some disks could not be deleted: %s", strings.Join(errs, "; "))
	}
	return removed, nil
}

// GuestInfo queries the QEMU guest agent. It never invents data: if the agent
// does not answer, Available is false.
func (m *Manager) GuestInfo(name string) (*agentapi.GuestInfo, error) {
	l, d, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	if st, _, _ := l.DomainGetState(d, 0); stateName(st) != "running" {
		return &agentapi.GuestInfo{Available: false, Error: "vm_not_running"}, nil
	}
	ifs, err := l.DomainInterfaceAddresses(d, uint32(libvirt.DomainInterfaceAddressesSrcAgent), 0)
	if err != nil {
		return &agentapi.GuestInfo{Available: false, Error: "agent_unavailable"}, nil
	}
	gi := &agentapi.GuestInfo{Available: true, Interfaces: []agentapi.GuestInterface{}}
	for _, i := range ifs {
		if i.Name == "lo" {
			continue
		}
		g := agentapi.GuestInterface{Name: i.Name, Addresses: []string{}}
		if len(i.Hwaddr) > 0 {
			g.MAC = i.Hwaddr[0]
		}
		for _, a := range i.Addrs {
			g.Addresses = append(g.Addresses, fmt.Sprintf("%s/%d", a.Addr, a.Prefix))
		}
		gi.Interfaces = append(gi.Interfaces, g)
	}
	return gi, nil
}

// VNCSocket returns the UNIX socket path of a running VM's VNC server.
func (m *Manager) VNCSocket(name string) (string, error) {
	l, d, err := m.lookup(name)
	if err != nil {
		return "", err
	}
	if st, _, _ := l.DomainGetState(d, 0); stateName(st) != "running" && stateName(st) != "paused" {
		return "", fmt.Errorf("%w: VM is not running", ErrConflict)
	}
	x, _, err := m.domainXML(l, d, false)
	if err != nil {
		return "", err
	}
	if x.Devices != nil {
		for _, g := range x.Devices.Graphics {
			if g.VNC == nil {
				continue
			}
			if g.VNC.Socket != "" {
				return g.VNC.Socket, nil
			}
			for _, li := range g.VNC.Listeners {
				if li.Socket != nil && li.Socket.Socket != "" {
					return li.Socket.Socket, nil
				}
			}
		}
	}
	return "", fmt.Errorf("%w: VM has no VNC socket console", ErrConflict)
}
