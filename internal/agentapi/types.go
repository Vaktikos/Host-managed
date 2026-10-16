// Package agentapi defines the typed contract between the control plane and
// the node agent. The agent only accepts these operations; it never executes
// free-form commands.
package agentapi

import "time"

// Host describes the node the agent runs on.
type Host struct {
	Hostname       string      `json:"hostname"`
	Kernel         string      `json:"kernel"`
	OS             string      `json:"os"`
	Arch           string      `json:"arch"`
	CPUModel       string      `json:"cpu_model"`
	CPUThreads     int         `json:"cpu_threads"`
	CPUSockets     int         `json:"cpu_sockets"`
	VirtExtensions string      `json:"virt_extensions"` // "vmx", "svm" or ""
	KVMAvailable   bool        `json:"kvm_available"`
	IOMMUEnabled   bool        `json:"iommu_enabled"`
	BootMode       string      `json:"boot_mode"` // "uefi" or "bios"
	MemTotal       uint64      `json:"mem_total"`
	MemAvailable   uint64      `json:"mem_available"`
	SwapTotal      uint64      `json:"swap_total"`
	SwapFree       uint64      `json:"swap_free"`
	Load           [3]float64  `json:"load"`
	CPUUsage       *float64    `json:"cpu_usage"` // percent, nil until two samples exist
	UptimeSeconds  uint64      `json:"uptime_seconds"`
	LibvirtVersion string      `json:"libvirt_version"`
	QEMUVersion    string      `json:"qemu_version"`
	LXCVersion     string      `json:"lxc_version"`
	AgentVersion   string      `json:"agent_version"`
	Filesystems    []HostMount `json:"filesystems"`
	Time           time.Time   `json:"time"`
}

type HostMount struct {
	Mountpoint string `json:"mountpoint"`
	Device     string `json:"device"`
	FSType     string `json:"fstype"`
	Total      uint64 `json:"total"`
	Free       uint64 `json:"free"`
}

// VM is a summary of a libvirt domain.
type VM struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	State       string   `json:"state"` // running, paused, shutoff, crashed, suspended, shutdown, blocked, unknown
	VCPUs       int      `json:"vcpus"`
	MemoryMiB   uint64   `json:"memory_mib"`
	Autostart   bool     `json:"autostart"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Template    bool     `json:"template"`
}

// VMDetail is the full view of a domain.
type VMDetail struct {
	VM
	Notes      string    `json:"notes"`
	DomainType string    `json:"domain_type"` // kvm or qemu (software emulation)
	Machine    string    `json:"machine"`
	Firmware   string    `json:"firmware"` // bios or uefi
	SecureBoot bool      `json:"secure_boot"`
	TPM        bool      `json:"tpm"`
	CPUMode    string    `json:"cpu_mode"`
	Disks      []VMDisk  `json:"disks"`
	NICs       []VMNIC   `json:"nics"`
	BootOrder  []string  `json:"boot_order"`
	Console    VMConsole `json:"console"`
	GuestAgent bool      `json:"guest_agent_channel"`
	XML        string    `json:"-"`
}

type VMDisk struct {
	Target   string `json:"target"` // vda, sda ...
	Device   string `json:"device"` // disk, cdrom
	Bus      string `json:"bus"`
	Source   string `json:"source"`
	Format   string `json:"format"`
	Capacity uint64 `json:"capacity,omitempty"`
	ReadOnly bool   `json:"readonly"`
}

type VMNIC struct {
	MAC    string `json:"mac"`
	Model  string `json:"model"`
	Type   string `json:"type"` // network or bridge
	Source string `json:"source"`
	Target string `json:"target,omitempty"` // host-side tap device when running
}

type VMConsole struct {
	VNC bool `json:"vnc"`
}

// VMStats are live counters. Rates are nil when no previous sample exists.
type VMStats struct {
	State         string    `json:"state"`
	CPUUsage      *float64  `json:"cpu_usage"` // percent of allocated vCPUs
	MemoryMiB     uint64    `json:"memory_mib"`
	MemoryUsedKiB *uint64   `json:"memory_used_kib"` // from balloon driver, if reported
	NetRxBps      *float64  `json:"net_rx_bps"`
	NetTxBps      *float64  `json:"net_tx_bps"`
	DiskReadBps   *float64  `json:"disk_read_bps"`
	DiskWriteBps  *float64  `json:"disk_write_bps"`
	Time          time.Time `json:"time"`
}

// GuestInfo comes from the QEMU guest agent. Available is false when the
// agent did not answer; in that case no other field is filled.
type GuestInfo struct {
	Available  bool             `json:"available"`
	Error      string           `json:"error,omitempty"`
	Interfaces []GuestInterface `json:"interfaces,omitempty"`
}

type GuestInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	Addresses []string `json:"addresses"`
}

// VMCreate is the validated request for a new VM.
type VMCreate struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	VCPUs       int      `json:"vcpus"`
	MemoryMiB   uint64   `json:"memory_mib"`
	CPUMode     string   `json:"cpu_mode"` // host-passthrough, host-model, qemu64
	Firmware    string   `json:"firmware"` // bios, uefi
	SecureBoot  bool     `json:"secure_boot"`
	TPM         bool     `json:"tpm"`
	DiskPool    string   `json:"disk_pool"`
	DiskSizeGiB uint64   `json:"disk_size_gib"`
	DiskBus     string   `json:"disk_bus"` // virtio, sata, scsi
	ISOPool     string   `json:"iso_pool,omitempty"`
	ISOVolume   string   `json:"iso_volume,omitempty"`
	Network     string   `json:"network"`   // "network:<name>" or "bridge:<name>"
	NICModel    string   `json:"nic_model"` // virtio, e1000
	GuestAgent  bool     `json:"guest_agent"`
	Autostart   bool     `json:"autostart"`
	Start       bool     `json:"start"`
}

// VMMeta updates description, tags and notes stored in the domain metadata.
type VMMeta struct {
	Description *string  `json:"description,omitempty"`
	Notes       *string  `json:"notes,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Autostart   *bool    `json:"autostart,omitempty"`
}

// VMResources changes CPU and memory. Applied to the persistent config; takes
// effect on next start.
type VMResources struct {
	VCPUs     int    `json:"vcpus"`
	MemoryMiB uint64 `json:"memory_mib"`
}

type Action struct {
	Action string `json:"action"`
}

// StoragePool is a libvirt storage pool.
type StoragePool struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	State      string `json:"state"`
	Active     bool   `json:"active"`
	Autostart  bool   `json:"autostart"`
	Path       string `json:"path"`
	Capacity   uint64 `json:"capacity"`
	Allocation uint64 `json:"allocation"`
	Available  uint64 `json:"available"`
	Content    string `json:"content"` // images, iso, mixed — from pool name convention
}

type StorageVolume struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	Format     string `json:"format"`
	Capacity   uint64 `json:"capacity"`
	Allocation uint64 `json:"allocation"`
}

// Interface is a host network interface.
type Interface struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"` // physical, bridge, bond, vlan, virtual, loopback
	MAC       string   `json:"mac"`
	MTU       int      `json:"mtu"`
	State     string   `json:"state"` // up, down, unknown
	Speed     int      `json:"speed_mbps,omitempty"`
	Addresses []string `json:"addresses"`
	Master    string   `json:"master,omitempty"`
	Members   []string `json:"members,omitempty"`
	RxBytes   uint64   `json:"rx_bytes"`
	TxBytes   uint64   `json:"tx_bytes"`
	RxErrors  uint64   `json:"rx_errors"`
	TxErrors  uint64   `json:"tx_errors"`
	RxDropped uint64   `json:"rx_dropped"`
	TxDropped uint64   `json:"tx_dropped"`
}

type VirtNetwork struct {
	Name      string `json:"name"`
	UUID      string `json:"uuid"`
	Active    bool   `json:"active"`
	Autostart bool   `json:"autostart"`
	Bridge    string `json:"bridge"`
	Forward   string `json:"forward"` // nat, route, bridge, isolated
	IPv4      string `json:"ipv4,omitempty"`
}

// Container is an LXC container.
type Container struct {
	Name         string   `json:"name"`
	State        string   `json:"state"` // running, stopped, frozen, ...
	Autostart    bool     `json:"autostart"`
	Unprivileged bool     `json:"unprivileged"`
	PID          int      `json:"pid,omitempty"`
	IPs          []string `json:"ips"`
	MemoryLimit  uint64   `json:"memory_limit,omitempty"` // bytes, 0 = unlimited
	CPULimit     float64  `json:"cpu_limit,omitempty"`    // cores, 0 = unlimited
	MemoryUsage  *uint64  `json:"memory_usage,omitempty"`
	Hostname     string   `json:"hostname,omitempty"`
	Distribution string   `json:"distribution,omitempty"`
	Release      string   `json:"release,omitempty"`
	Description  string   `json:"description,omitempty"`
}

type ContainerCreate struct {
	Name         string  `json:"name"`
	Distribution string  `json:"distribution"`
	Release      string  `json:"release"`
	Arch         string  `json:"arch"`
	MemoryMiB    uint64  `json:"memory_mib"`
	SwapMiB      uint64  `json:"swap_mib"`
	CPULimit     float64 `json:"cpu_limit"`
	Bridge       string  `json:"bridge"`
	IPv4         string  `json:"ipv4,omitempty"` // CIDR, empty = DHCP
	Gateway4     string  `json:"gateway4,omitempty"`
	Autostart    bool    `json:"autostart"`
	Start        bool    `json:"start"`
	Description  string  `json:"description,omitempty"`
}

// Error is the agent's error body.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Download asks the agent to fetch a file into a directory pool.
type Download struct {
	URL      string `json:"url"`
	Name     string `json:"name"`
	Checksum string `json:"checksum,omitempty"`
}

// DiskAdd adds a new empty disk to a VM.
type DiskAdd struct {
	Pool    string `json:"pool"`
	SizeGiB uint64 `json:"size_gib"`
	Bus     string `json:"bus"` // virtio, scsi, sata
}

// DiskResize grows a disk. Shrinking is not supported.
type DiskResize struct {
	SizeGiB uint64 `json:"size_gib"`
}

// Media inserts an ISO into a CD-ROM drive; empty fields eject.
type Media struct {
	Pool   string `json:"pool,omitempty"`
	Volume string `json:"volume,omitempty"`
}

// NICAdd adds a network card.
type NICAdd struct {
	Network string `json:"network"` // network:<name> or bridge:<name>
	Model   string `json:"model"`
}

// BootOrder lists disk targets ("vda") and NICs ("net:<mac>") in order.
type BootOrder struct {
	Order []string `json:"order"`
}

// ChangeResult tells whether a change waits for the next VM start.
type ChangeResult struct {
	PendingRestart bool   `json:"pending_restart"`
	Target         string `json:"target,omitempty"`
}

// Snapshot of a VM.
type Snapshot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"` // unix seconds
	State       string `json:"state"`      // VM state at snapshot time
	Memory      bool   `json:"memory"`     // includes RAM state
	Parent      string `json:"parent,omitempty"`
	Current     bool   `json:"current"`
}

type SnapshotCreate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Clone creates a copy of a shut-off VM or a template.
type Clone struct {
	Name   string `json:"name"`
	Mode   string `json:"mode"` // full or linked (linked: templates in qcow2 directory pools only)
	Pool   string `json:"pool,omitempty"`
	Start  bool   `json:"start"`
}
