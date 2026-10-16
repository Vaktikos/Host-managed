# Operations guide

Describes the version currently in the repository. Planned features are listed in [status.md](status.md).

## Virtual machines

**Create** (web UI: *Virtual machines → Create VM*, CLI: `hostctl vm create`):

| Setting | Notes |
|---|---|
| Name | 1–63 characters, letters, digits, `.`, `_`, `-`; unique |
| vCPUs, memory | can be changed later (applies at next start) |
| CPU mode | `host-passthrough` (default, best performance), `host-model` (better for migration), `qemu64` (widest compatibility) |
| Firmware | UEFI (OVMF, default) or BIOS (SeaBIOS); Secure Boot only with UEFI and KVM |
| TPM 2.0 | emulated by swtpm (e.g. for Windows 11) |
| Disk | size in GiB, pool, bus VirtIO block (default), VirtIO SCSI or SATA; qcow2 with thin provisioning in directory pools |
| Installation medium | ISO from a pool with content "ISO" (pool `iso`; upload under *Storage*) |
| Network | libvirt network (e.g. `default`, NAT) or a host Linux bridge |
| Guest agent channel | channel for `qemu-guest-agent`; needed for guest IPs and clean shutdown |

Boot order: disk before CD-ROM. An empty disk is skipped and the medium boots. After installation the VM boots from disk without removing the ISO.

**Power**: start, shut down (ACPI), reboot, pause/resume, power off (immediate, confirmed), hard reset (confirmed). Every action is written to the audit log.

**Console**: VNC in the browser (noVNC) with Ctrl+Alt+Del and scaling. The connection goes through the signed-in session; VNC is not reachable over TCP on the host.

**Summary**: CPU, memory, network and disk throughput of the last 10 minutes (sampled every 5 s by the agent). Guest memory usage only if the balloon driver reports it. Guest IPs only if the QEMU guest agent runs in the guest; otherwise "does not respond" is shown and nothing is estimated.

**Notes and options**: description, tags, notes, autostart. VMs marked as template cannot be started.

**Delete**: only when powered off, confirmed by typing the name. Optionally deletes the VM's disks (only volumes in libvirt pools; ISO media never).

Not possible in this version: adding or changing disks/NICs after creation, snapshots, cloning, cloud-init, PCI passthrough, migration. See [status.md](status.md).

## Containers (LXC)

**Create**: image (distribution/release) from the official list at `images.linuxcontainers.org`, memory, swap and CPU limits, bridge (e.g. `virbr0` for the NAT network), static IPv4 with gateway or DHCP.

Containers are always **unprivileged**: root in the container is UID 100000 on the host (user namespace), AppArmor profile `generated`, nesting disabled.

**Console**: browser terminal that opens a root login inside the container (`lxc-attach … /bin/login -f root`). The `ct.console` permission therefore equals root access inside the container.

Limits and autostart can be changed under *Summary → Edit*; they apply from the next start.

## Storage

All libvirt storage pools are listed with type, state, capacity and usage. Pools named `iso` (or ending in `-iso`) are treated as ISO stores, all others as VM disk stores. Files can be uploaded into directory pools (default limit 64 GiB, `max_upload_gib`). Volumes used by a VM cannot be deleted.

Additional pools (LVM thin, ZFS, NFS …) can currently be defined with `virsh pool-define` and appear automatically. VM disks are created in pools of type `dir`, `fs`, `netfs` (qcow2) and `logical`, `zfs`, `rbd` (raw); only `dir` is tested.

## Network

Host interfaces (kind, addresses, bridge members, MTU, speed, counters) and libvirt networks are shown. Changes are made outside the UI in this version.

### Setting up a bridge

Only with console access to the server (provider rescue system, KVM/IPMI), because a mistake cuts the connection. Example for Debian with `ifupdown` (`/etc/network/interfaces`), NIC `eno1`, address previously on `eno1`:

```
auto lo
iface lo inet loopback

iface eno1 inet manual

auto vmbr0
iface vmbr0 inet static
    address 203.0.113.10/24
    gateway 203.0.113.1
    bridge-ports eno1
    bridge-stp off
    bridge-fd 0
```

Then `apt install bridge-utils` (if needed) and `systemctl restart networking` or reboot. The bridge appears in the VM dialog as "Bridge vmbr0". Many providers (e.g. Hetzner) require a separate MAC address from their portal or a routed setup for additional IPs.

## Users and roles

| Role | Rights |
|---|---|
| Administrator (`admin`) | everything |
| VM administrator (`vm-admin`) | VMs and containers: view, power, console, configure, create, delete; upload ISOs |
| Operator (`operator`) | view, power and console for VMs and containers |
| Storage administrator (`storage-admin`) | view, upload and delete storage |
| Network administrator (`network-admin`) | view network |
| Read-only (`auditor`) | view everything, read the audit log, change nothing |

Roles apply to the whole node. The individual permissions are listed in the UI under *Users → Roles*. The last active administrator cannot be deleted or demoted.

Passwords: at least 12 characters; Argon2id. After 8 failures per user or 20 per IP address, sign-in is blocked for 15 minutes. Sessions expire after 2 hours of inactivity and at the latest after 12 hours (configurable).

## Tasks and audit

Long operations (create/delete VM or container) run as tasks with a log. No percentage is estimated; the UI shows "Running". Tasks running during a service restart are marked "interrupted". Tasks older than 90 days are removed.

The audit log records user, time, source IP, action, resource, result, correlation ID and details of all sign-ins, changes and denied requests. *Verify integrity* checks the hash chain.

## Command line (hostctl)

Create an API token under *Account → API tokens*, then:

```bash
export HM_URL=https://server:8443
export HM_TOKEN=hm_...
export HM_FINGERPRINT=0F:26:...     # fingerprint printed by the installer (self-signed certificate)

hostctl node show
hostctl vm list
hostctl vm create --name web1 --pool local --vcpus 2 --memory 4096 --disk 40 --iso iso/debian-13.iso --start --wait
hostctl vm shutdown web1
hostctl vm delete web1 --yes --delete-disks --wait
hostctl ct list
hostctl storage list
hostctl job list
hostctl --json vm list
```

Help is shown in German when `LANG` starts with `de`.

## REST API

Description: [`api/openapi.yaml`](../../api/openapi.yaml). Authenticate with `Authorization: Bearer hm_…` (API token) or the session cookie plus `X-CSRF-Token`. Errors always look like:

```json
{"error": {"code": "validation", "message": "…", "field": "name", "request_id": "…"}}
```

Long operations answer `202 Accepted` with a job object; status at `GET /api/v1/jobs/{id}`, log at `GET /api/v1/jobs/{id}/log?after=<id>`.
