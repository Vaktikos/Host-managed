// Types mirror the Go structs in internal/agentapi and internal/store.

export interface User {
  id: number
  username: string
  role: string
  language: 'de' | 'en'
  disabled: boolean
  created_at: string
  password_changed_at: string
}

export interface Me {
  user: User
  permissions: string[]
  csrf_token?: string
  product: string
  version: string
}

export interface HostMount {
  mountpoint: string
  device: string
  fstype: string
  total: number
  free: number
}

export interface Host {
  hostname: string
  kernel: string
  os: string
  arch: string
  cpu_model: string
  cpu_threads: number
  cpu_sockets: number
  virt_extensions: string
  kvm_available: boolean
  iommu_enabled: boolean
  boot_mode: string
  mem_total: number
  mem_available: number
  swap_total: number
  swap_free: number
  load: [number, number, number]
  cpu_usage: number | null
  uptime_seconds: number
  libvirt_version: string
  qemu_version: string
  lxc_version: string
  agent_version: string
  filesystems: HostMount[]
  time: string
}

export interface NodeInfo {
  host: Host
  control_plane: { version: string; schema_version: number; allow_software_emulation: boolean }
}

export interface VMStats {
  state: string
  cpu_usage: number | null
  memory_mib: number
  memory_used_kib: number | null
  net_rx_bps: number | null
  net_tx_bps: number | null
  disk_read_bps: number | null
  disk_write_bps: number | null
  time: string
}

export interface VM {
  uuid: string
  name: string
  state: string
  vcpus: number
  memory_mib: number
  autostart: boolean
  description: string
  tags: string[]
  template: boolean
  stats?: VMStats
}

export interface VMDisk {
  target: string
  device: string
  bus: string
  source: string
  format: string
  capacity?: number
  readonly: boolean
}

export interface VMNIC {
  mac: string
  model: string
  type: string
  source: string
  target?: string
}

export interface VMDetail extends VM {
  notes: string
  domain_type: string
  machine: string
  firmware: string
  secure_boot: boolean
  tpm: boolean
  cpu_mode: string
  disks: VMDisk[]
  nics: VMNIC[]
  boot_order: string[] | null
  console: { vnc: boolean }
  guest_agent_channel: boolean
  cloud_init?: CloudInit
}

export interface CloudInit {
  user: string
  password?: string
  password_hash?: string // "set" when a password exists
  ssh_keys?: string[]
  hostname?: string
  ipv4?: string
  gateway4?: string
  ipv6?: string
  gateway6?: string
  dns?: string[]
  packages?: string[]
  guest_agent: boolean
}

export interface GuestInfo {
  available: boolean
  error?: string
  interfaces?: { name: string; mac: string; addresses: string[] }[]
}

export interface VMCreate {
  name: string
  description: string
  tags: string[]
  vcpus: number
  memory_mib: number
  cpu_mode: string
  firmware: string
  secure_boot: boolean
  tpm: boolean
  disk_pool: string
  disk_size_gib: number
  disk_bus: string
  iso_pool?: string
  iso_volume?: string
  network: string
  nic_model: string
  guest_agent: boolean
  autostart: boolean
  start: boolean
  image_pool?: string
  image_volume?: string
  cloud_init?: CloudInit
}

export interface StoragePool {
  name: string
  type: string
  state: string
  active: boolean
  autostart: boolean
  path: string
  capacity: number
  allocation: number
  available: number
  content: string
}

export interface StorageVolume {
  name: string
  path: string
  type: string
  format: string
  capacity: number
  allocation: number
}

export interface Interface {
  name: string
  kind: string
  mac: string
  mtu: number
  state: string
  speed_mbps?: number
  addresses: string[]
  master?: string
  members?: string[]
  rx_bytes: number
  tx_bytes: number
  rx_errors: number
  tx_errors: number
  rx_dropped: number
  tx_dropped: number
}

export interface VirtNetwork {
  name: string
  uuid: string
  active: boolean
  autostart: boolean
  bridge: string
  forward: string
  ipv4?: string
}

export interface Container {
  name: string
  state: string
  autostart: boolean
  unprivileged: boolean
  pid?: number
  ips: string[]
  memory_limit?: number
  cpu_limit?: number
  memory_usage?: number
  hostname?: string
  distribution?: string
  release?: string
  description?: string
}

export interface ContainerCreate {
  name: string
  distribution: string
  release: string
  arch: string
  memory_mib: number
  swap_mib: number
  cpu_limit: number
  bridge: string
  ipv4?: string
  gateway4?: string
  autostart: boolean
  start: boolean
  description?: string
}

export interface LXCImage {
  distribution: string
  release: string
  arch: string
  build: string
}

export interface Job {
  id: string
  type: string
  target: string
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'
  username: string
  created_at: string
  started_at: string | null
  finished_at: string | null
  error: string
  progress: number | null
}

export interface LogLine {
  id: number
  time: string
  line: string
}

export interface AuditEntry {
  id: number
  time: string
  username: string
  ip: string
  action: string
  resource: string
  result: string
  correlation_id: string
  detail: string
}

export interface APIToken {
  id: string
  name: string
  created_at: string
  expires_at: string | null
  last_used: string | null
}

export interface SessionInfo {
  id: string
  created_at: string
  last_seen: string
  expires_at: string
  ip: string
  user_agent: string
  current: boolean
}

export interface RoleInfo {
  name: string
  permissions: string[]
}
