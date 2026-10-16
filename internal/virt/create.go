package virt

import (
	"fmt"
	"os"
	"path/filepath"

	libvirt "github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"

	"github.com/vaktikos/host-managed/internal/agentapi"
)

// hotplugPorts is the number of spare PCIe root ports added to new VMs.
const hotplugPorts = 6

// diskTargets returns the device names for the system disk and the CD-ROM.
func diskTargets(bus string) (disk, cdrom string) {
	switch bus {
	case "sata", "scsi":
		return "sda", "sdb"
	}
	return "vda", "sda"
}

// BuildDomainXML renders the libvirt definition for a new VM. diskPath and
// diskFormat refer to an already created volume; isoPath may be empty.
func BuildDomainXML(c *agentapi.VMCreate, domType, diskPath, diskFormat, isoPath string) (string, error) {
	return buildDomainXML(c, domType, diskPath, diskFormat, isoPath, "", "", "")
}

// buildDomainXML additionally sets a fixed UUID and attaches a cloud-init
// seed as second CD-ROM drive.
func buildDomainXML(c *agentapi.VMCreate, domType, diskPath, diskFormat, isoPath, uuid, mac, seedPath string) (string, error) {
	diskDev, cdDev := diskTargets(c.DiskBus)

	dom := &libvirtxml.Domain{
		Type:          domType,
		Name:          c.Name,
		UUID:          uuid,
		Memory:        &libvirtxml.DomainMemory{Value: uint(c.MemoryMiB), Unit: "MiB"},
		CurrentMemory: &libvirtxml.DomainCurrentMemory{Value: uint(c.MemoryMiB), Unit: "MiB"},
		VCPU:          &libvirtxml.DomainVCPU{Placement: "static", Value: uint(c.VCPUs)},
		OS: &libvirtxml.DomainOS{
			Type: &libvirtxml.DomainOSType{Arch: "x86_64", Machine: "q35", Type: "hvm"},
		},
		Features: &libvirtxml.DomainFeatureList{
			ACPI: &libvirtxml.DomainFeature{},
			APIC: &libvirtxml.DomainFeatureAPIC{},
		},
		Clock: &libvirtxml.DomainClock{
			Offset: "utc",
			Timer: []libvirtxml.DomainTimer{
				{Name: "rtc", TickPolicy: "catchup"},
				{Name: "pit", TickPolicy: "delay"},
				{Name: "hpet", Present: "no"},
			},
		},
		OnPoweroff: "destroy",
		OnReboot:   "restart",
		OnCrash:    "destroy",
		Devices:    &libvirtxml.DomainDeviceList{},
	}

	if domType == "kvm" {
		dom.CPU = &libvirtxml.DomainCPU{Mode: c.CPUMode}
		if c.CPUMode == "qemu64" {
			dom.CPU = &libvirtxml.DomainCPU{Mode: "custom", Match: "exact", Model: &libvirtxml.DomainCPUModel{Value: "qemu64", Fallback: "allow"}}
		} else if c.CPUMode == "max" {
			dom.CPU = &libvirtxml.DomainCPU{Mode: "maximum"}
		}
	}

	if c.Firmware == "uefi" {
		dom.OS.Firmware = "efi"
		sb := "no"
		if c.SecureBoot {
			sb = "yes"
			dom.Features.SMM = &libvirtxml.DomainFeatureSMM{State: "on"}
			dom.OS.Loader = &libvirtxml.DomainLoader{Secure: "yes"}
		}
		dom.OS.FirmwareInfo = &libvirtxml.DomainOSFirmwareInfo{
			Features: []libvirtxml.DomainOSFirmwareFeature{
				{Name: "secure-boot", Enabled: sb},
				{Name: "enrolled-keys", Enabled: sb},
			},
		}
	}

	dev := dom.Devices

	if c.DiskBus == "scsi" {
		dev.Controllers = append(dev.Controllers, libvirtxml.DomainController{Type: "scsi", Model: "virtio-scsi"})
	}
	dev.Disks = append(dev.Disks, libvirtxml.DomainDisk{
		Device: "disk",
		Driver: &libvirtxml.DomainDiskDriver{Name: "qemu", Type: diskFormat, Discard: "unmap", Cache: "none"},
		Source: diskSource(diskPath),
		Target: &libvirtxml.DomainDiskTarget{Dev: diskDev, Bus: c.DiskBus},
		Boot:   &libvirtxml.DomainDeviceBoot{Order: 1},
	})
	cd := libvirtxml.DomainDisk{
		Device:   "cdrom",
		Driver:   &libvirtxml.DomainDiskDriver{Name: "qemu", Type: "raw"},
		Target:   &libvirtxml.DomainDiskTarget{Dev: cdDev, Bus: "sata"},
		ReadOnly: &libvirtxml.DomainDiskReadOnly{},
		Boot:     &libvirtxml.DomainDeviceBoot{Order: 2},
	}
	if isoPath != "" {
		cd.Source = &libvirtxml.DomainDiskSource{File: &libvirtxml.DomainDiskSourceFile{File: isoPath}}
	}
	dev.Disks = append(dev.Disks, cd)
	if seedPath != "" {
		seedTarget := "sdb"
		if cdDev == "sdb" {
			seedTarget = "sdc"
		}
		dev.Disks = append(dev.Disks, seedDisk(seedPath, seedTarget))
	}

	kind, netName, err := agentapi.ParseNetworkRef(c.Network)
	if err != nil {
		return "", err
	}
	nic := libvirtxml.DomainInterface{Model: &libvirtxml.DomainInterfaceModel{Type: c.NICModel}}
	if mac != "" {
		nic.MAC = &libvirtxml.DomainInterfaceMAC{Address: mac}
	}
	if kind == "network" {
		nic.Source = &libvirtxml.DomainInterfaceSource{Network: &libvirtxml.DomainInterfaceSourceNetwork{Network: netName}}
	} else {
		nic.Source = &libvirtxml.DomainInterfaceSource{Bridge: &libvirtxml.DomainInterfaceSourceBridge{Bridge: netName}}
	}
	dev.Interfaces = append(dev.Interfaces, nic)

	port := uint(0)
	dev.Serials = append(dev.Serials, libvirtxml.DomainSerial{
		Source: &libvirtxml.DomainChardevSource{Pty: &libvirtxml.DomainChardevSourcePty{}},
		Target: &libvirtxml.DomainSerialTarget{Port: &port},
	})
	dev.Consoles = append(dev.Consoles, libvirtxml.DomainConsole{
		Source: &libvirtxml.DomainChardevSource{Pty: &libvirtxml.DomainChardevSourcePty{}},
		Target: &libvirtxml.DomainConsoleTarget{Type: "serial", Port: &port},
	})
	if c.GuestAgent {
		dev.Channels = append(dev.Channels, libvirtxml.DomainChannel{
			Source: &libvirtxml.DomainChardevSource{UNIX: &libvirtxml.DomainChardevSourceUNIX{Mode: "bind"}},
			Target: &libvirtxml.DomainChannelTarget{VirtIO: &libvirtxml.DomainChannelTargetVirtIO{Name: "org.qemu.guest_agent.0"}},
		})
	}
	dev.Inputs = append(dev.Inputs,
		libvirtxml.DomainInput{Type: "tablet", Bus: "usb"},
		libvirtxml.DomainInput{Type: "keyboard", Bus: "usb"},
	)
	dev.Controllers = append(dev.Controllers, libvirtxml.DomainController{Type: "usb", Model: "qemu-xhci"})
	// Spare PCIe root ports so disks and NICs can be hot-plugged later; on
	// q35 every hot-plugged PCIe device needs a free port.
	// Indexes 0 (pcie-root) and the ports libvirt adds for the devices above
	// come first; ours start high enough not to collide.
	zero := uint(0)
	dev.Controllers = append(dev.Controllers, libvirtxml.DomainController{Type: "pci", Model: "pcie-root", Index: &zero})
	for i := 0; i < hotplugPorts; i++ {
		idx := uint(20 + i)
		dev.Controllers = append(dev.Controllers, libvirtxml.DomainController{Type: "pci", Model: "pcie-root-port", Index: &idx})
	}
	// VNC on a UNIX socket managed by libvirt: no TCP port is exposed and
	// only the agent (root) can reach it.
	dev.Graphics = append(dev.Graphics, libvirtxml.DomainGraphic{
		VNC: &libvirtxml.DomainGraphicVNC{
			Listeners: []libvirtxml.DomainGraphicListener{{Socket: &libvirtxml.DomainGraphicListenerSocket{}}},
		},
	})
	dev.Videos = append(dev.Videos, libvirtxml.DomainVideo{Model: libvirtxml.DomainVideoModel{Type: "vga", VRam: 16384}})
	dev.MemBalloon = &libvirtxml.DomainMemBalloon{Model: "virtio", Stats: &libvirtxml.DomainMemBalloonStats{Period: 10}}
	dev.RNGs = append(dev.RNGs, libvirtxml.DomainRNG{
		Model:   "virtio",
		Backend: &libvirtxml.DomainRNGBackend{Random: &libvirtxml.DomainRNGBackendRandom{Device: "/dev/urandom"}},
	})
	if c.TPM {
		dev.TPMs = append(dev.TPMs, libvirtxml.DomainTPM{
			Model:   "tpm-crb",
			Backend: &libvirtxml.DomainTPMBackend{Emulator: &libvirtxml.DomainTPMBackendEmulator{Version: "2.0"}},
		})
	}
	return dom.Marshal()
}

func diskSource(path string) *libvirtxml.DomainDiskSource {
	return &libvirtxml.DomainDiskSource{File: &libvirtxml.DomainDiskSourceFile{File: path}}
}

// CreateVM creates the disk volume, defines the domain and optionally starts
// it. On failure everything created so far is removed again. The log
// callback receives human-readable progress lines for the job log.
func (m *Manager) CreateVM(c agentapi.VMCreate, log func(string)) (*agentapi.VMDetail, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	l, err := m.conn()
	if err != nil {
		return nil, err
	}
	if _, err := l.DomainLookupByName(c.Name); err == nil {
		return nil, fmt.Errorf("%w: a VM named %q already exists", ErrConflict, c.Name)
	}

	domType := "kvm"
	if !KVMAvailable() {
		if !m.allowTCG {
			return nil, fmt.Errorf("%w: /dev/kvm is not available (enable virtualization in BIOS/UEFI or set allow_software_emulation)", ErrConflict)
		}
		domType = "qemu"
		log("KVM not available: using software emulation (TCG), which is much slower")
	}
	if c.SecureBoot && domType != "kvm" {
		return nil, fmt.Errorf("%w: Secure Boot requires KVM (SMM emulation)", ErrConflict)
	}

	pool, err := l.StoragePoolLookupByName(c.DiskPool)
	if err != nil {
		return nil, wrap(err)
	}
	poolX, err := poolDef(l, pool)
	if err != nil {
		return nil, err
	}
	format, volName := "qcow2", c.Name+"-disk0.qcow2"
	switch poolX.Type {
	case "dir", "fs", "netfs":
	case "logical", "zfs", "rbd", "iscsi-direct":
		format, volName = "raw", c.Name+"-disk0"
	default:
		return nil, fmt.Errorf("%w: storage pool type %q is not supported for VM disks yet", ErrConflict, poolX.Type)
	}

	var isoPath string
	if c.ISOVolume != "" {
		ip, err := l.StoragePoolLookupByName(c.ISOPool)
		if err != nil {
			return nil, wrap(err)
		}
		iv, err := l.StorageVolLookupByName(ip, c.ISOVolume)
		if err != nil {
			return nil, wrap(err)
		}
		if isoPath, err = l.StorageVolGetPath(iv); err != nil {
			return nil, wrap(err)
		}
		log("Installation medium: " + isoPath)
	}

	var vol libvirt.StorageVol
	if c.ImageVolume != "" {
		// Copy a disk image (e.g. a cloud image) as the system disk.
		ip, err := l.StoragePoolLookupByName(c.ImagePool)
		if err != nil {
			return nil, wrap(err)
		}
		src, err := l.StorageVolLookupByName(ip, c.ImageVolume)
		if err != nil {
			return nil, wrap(err)
		}
		_, srcCap, _, err := l.StorageVolGetInfo(src)
		if err != nil {
			return nil, wrap(err)
		}
		size := c.DiskSizeGiB << 30
		if size < srcCap {
			return nil, &agentapi.FieldError{Field: "disk_size_gib", Reason: fmt.Sprintf("the image needs at least %d GiB", (srcCap+(1<<30)-1)>>30)}
		}
		raw, _ := (&libvirtxml.StorageVolume{
			Name:     volName,
			Capacity: &libvirtxml.StorageVolumeSize{Unit: "bytes", Value: srcCap},
			Target:   &libvirtxml.StorageVolumeTarget{Format: &libvirtxml.StorageVolumeTargetFormat{Type: format}},
		}).Marshal()
		log(fmt.Sprintf("Copying image %s/%s to %s", c.ImagePool, c.ImageVolume, volName))
		if vol, err = l.StorageVolCreateXMLFrom(pool, raw, src, 0); err != nil {
			return nil, fmt.Errorf("copy image: %w", wrap(err))
		}
		if size > srcCap {
			log(fmt.Sprintf("Growing disk to %d GiB", c.DiskSizeGiB))
			if err := l.StorageVolResize(vol, size, 0); err != nil {
				_ = l.StorageVolDelete(vol, 0)
				return nil, fmt.Errorf("grow disk: %w", wrap(err))
			}
		}
	} else {
		volX := &libvirtxml.StorageVolume{
			Name:     volName,
			Capacity: &libvirtxml.StorageVolumeSize{Unit: "bytes", Value: c.DiskSizeGiB << 30},
			Target:   &libvirtxml.StorageVolumeTarget{Format: &libvirtxml.StorageVolumeTargetFormat{Type: format}},
		}
		if format == "qcow2" {
			volX.Allocation = &libvirtxml.StorageVolumeSize{Unit: "bytes", Value: 0}
		}
		volXML, err := volX.Marshal()
		if err != nil {
			return nil, err
		}
		log(fmt.Sprintf("Creating disk %s (%d GiB, %s) in pool %s", volName, c.DiskSizeGiB, format, c.DiskPool))
		if vol, err = l.StorageVolCreateXML(pool, volXML, 0); err != nil {
			return nil, fmt.Errorf("create disk: %w", wrap(err))
		}
	}
	var seedPath string
	cleanupVol := func() {
		if err := l.StorageVolDelete(vol, 0); err != nil {
			log("Cleanup: could not delete disk: " + err.Error())
		} else {
			log("Cleanup: disk removed")
		}
		removeFile(seedPath)
	}
	diskPath, err := l.StorageVolGetPath(vol)
	if err != nil {
		cleanupVol()
		return nil, wrap(err)
	}

	uuid, mac := newUUID(), ""
	meta := hmMeta{Description: c.Description, Tags: c.Tags}
	if c.CloudInit != nil {
		ci := *c.CloudInit
		if err := prepareCloudInit(&ci, ""); err != nil {
			cleanupVol()
			return nil, err
		}
		dir, seedPool, err := seedDir(l, c.DiskPool)
		if err != nil {
			cleanupVol()
			return nil, err
		}
		seedPath = filepath.Join(dir, c.Name+"-cloudinit.iso")
		if _, err := os.Lstat(seedPath); err == nil {
			seedPath = ""
			cleanupVol()
			return nil, fmt.Errorf("%w: %s-cloudinit.iso already exists", ErrConflict, c.Name)
		}
		log("Writing cloud-init seed " + seedPath)
		mac = newMAC()
		if err := writeSeed(seedPath, uuid, mac, ci); err != nil {
			cleanupVol()
			return nil, err
		}
		_ = l.StoragePoolRefresh(seedPool, 0)
		setMetaCloudInit(&meta, &ci, seedPath)
	}

	domXML, err := buildDomainXML(&c, domType, diskPath, format, isoPath, uuid, mac, seedPath)
	if err != nil {
		cleanupVol()
		return nil, err
	}
	log("Defining VM")
	d, err := l.DomainDefineXML(domXML)
	if err != nil {
		cleanupVol()
		return nil, fmt.Errorf("define VM: %w", wrap(err))
	}
	if err := m.writeMeta(l, d, meta); err != nil {
		log("Warning: could not store metadata: " + err.Error())
	}
	if c.Autostart {
		if err := l.DomainSetAutostart(d, 1); err != nil {
			log("Warning: could not enable autostart: " + err.Error())
		}
	}
	if c.Start {
		log("Starting VM")
		if err := l.DomainCreate(d); err != nil {
			// The VM stays defined so the user can fix the cause and start it.
			return nil, fmt.Errorf("VM defined, but start failed: %w", wrap(err))
		}
	}
	return m.GetVM(c.Name)
}

func poolDef(l *libvirt.Libvirt, p libvirt.StoragePool) (*libvirtxml.StoragePool, error) {
	raw, err := l.StoragePoolGetXMLDesc(p, 0)
	if err != nil {
		return nil, wrap(err)
	}
	var x libvirtxml.StoragePool
	if err := x.Unmarshal(raw); err != nil {
		return nil, err
	}
	return &x, nil
}
