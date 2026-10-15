# Architecture

This document describes the architecture of Host-managed, its technology decisions, trust boundaries and the planned extension to clustering, high availability and Proxmox Backup Server integration. What is implemented today is listed in [status.md](status.md).

## Overview

```
 Browser / hostctl / automation
        │  HTTPS (TLS 1.2+), session cookie + CSRF or API token
        ▼
 ┌───────────────────────────────────────────┐
 │ hostmanaged server  (user hostmanaged)     │  control plane
 │  REST API /api/v1, web UI, RBAC, audit,     │  – no root privileges
 │  jobs, SQLite                               │  – systemd hardening
 └──────────────┬────────────────────────────┘
                │  UNIX socket /run/hostmanaged/agent.sock
                │  (0660 root:hostmanaged, SO_PEERCRED check)
                │  typed operations, no shell access
                ▼
 ┌───────────────────────────────────────────┐
 │ hostmanaged agent  (root)                   │  node agent
 │  adapters: virt (libvirt RPC), lxc, hostinfo│
 └───────┬───────────────────┬────────────────┘
         │ libvirt-sock       │ lxc-* (fixed argv)
         ▼                    ▼
   libvirtd / QEMU / KVM     LXC (unprivileged, user namespaces)
```

| Component | Responsibility | Privileges | Code |
|---|---|---|---|
| Web frontend | React/TypeScript, German/English | browser | `web/` |
| Control plane | API, authentication, RBAC, jobs, audit, persistence | unprivileged user | `internal/api`, `internal/auth`, `internal/jobs`, `internal/store` |
| Node agent | the only root process; runs defined operations only | root | `internal/agent` |
| Virtualization adapter | KVM/QEMU via libvirt | inside agent | `internal/virt` |
| Container adapter | LXC | inside agent | `internal/lxc` |
| Host information | /proc, /sys | inside agent | `internal/hostinfo` |
| CLI | `hostctl` over the REST API | API token | `cmd/hostctl` |

## Technology decisions

| Area | Decision | Reason |
|---|---|---|
| Host OS | Debian 12/13 | Stable base with QEMU, libvirt, LXC, ZFS and Ceph from the archive; the same base as Proxmox VE, hence the same kernel and driver availability. |
| Hypervisor | KVM/QEMU via libvirt | Proven; supports live migration, snapshots, UEFI/OVMF, swtpm, PCI passthrough and storage pools. Driving QEMU directly (like Proxmox `qm`) is only needed for features libvirt cannot express. |
| libvirt binding | `github.com/digitalocean/go-libvirt` (pure Go RPC) + `libvirt.org/go/libvirtxml` | No cgo, static binary, reconnects after libvirtd restarts. |
| Containers | LXC tools (`lxc-create`, `lxc-start` …) | Standard for system containers; unprivileged with user namespaces. Always invoked with fixed argument vectors. |
| Backend and agent | Go | One static binary, no runtime dependencies, small footprint, good concurrency. |
| Frontend | React 18, TypeScript, Vite, i18next, noVNC, xterm.js | Widely used and maintained; small in-house components instead of a large UI kit to keep the design under control. |
| Persistence (single node) | SQLite (WAL) via `modernc.org/sqlite` | No external database on a single host. Versioned schema migrations. |
| Persistence (management server, planned) | PostgreSQL | For hundreds of nodes and multiple control plane instances. The store is a separate layer for that reason. |
| Jobs | own runner with persistent status in the database | Long operations outlive the HTTP request; after a restart, interrupted jobs are honestly marked "interrupted" instead of successful. |
| API | REST `/api/v1`, JSON, uniform error format, OpenAPI in `api/openapi.yaml` | Easy to automate; live data is polled (3–5 s) today, SSE is planned. |
| TLS | self-signed certificate (ECDSA P-256) at install time, fingerprint printed | Secure first start without an external CA; can be replaced at any time. |

## Data model

libvirt and LXC remain the source of truth for workloads. Host-managed does not keep a second copy of VM configuration that could drift:

- VM description, tags, notes and template flag live in the libvirt domain's `<metadata>` element (namespace `https://host-managed.dev/xmlns/vm/1`).
- Container metadata lives in `hostmanaged.json` next to the LXC config.
- The SQLite database only holds control plane data: users, sessions, API tokens, jobs with logs, audit log, settings.

## Trust boundaries

1. **Browser ↔ control plane**: HTTPS. Session cookie `HttpOnly`, `Secure`, `SameSite=Strict`; plus a CSRF token header on every write. Strict Content Security Policy. Permissions are only checked server-side.
2. **Control plane ↔ agent**: UNIX socket with mode 0660 (group `hostmanaged`) and an additional `SO_PEERCRED` UID check. The agent only accepts operations defined in `internal/agentapi` and validates every input again (it does not trust the control plane blindly).
3. **Agent ↔ libvirt/LXC**: libvirt via RPC; LXC tools with fixed argv, never through a shell. Names, distributions, bridges and addresses are checked against strict regular expressions or `netip` first.
4. **Consoles**: VNC does not listen on TCP but on a libvirt-managed UNIX socket that only the agent connects to. The browser reaches it through an authenticated, same-origin WebSocket.
5. **Database**: readable by user `hostmanaged` only. Passwords as Argon2id hashes, session and token secrets stored only as SHA-256 hashes. The audit log is append-only via triggers and additionally protected by a hash chain against edits made directly in the file.
6. **Backup system (planned)**: Proxmox Backup Server credentials stored encrypted (key outside the database), using least-privilege API tokens.

## Multi-node, cluster and HA (design, not implemented yet)

- The agent contract (`internal/agentapi`) is transport-independent. Remote nodes will serve the same contract over mutually authenticated TLS (mTLS); joining uses a one-time join token and certificate fingerprint verification.
- Membership and quorum are not reinvented: etcd (Raft) is planned for membership, configuration distribution and leases; alternatively corosync/votequorum as in Proxmox.
- **HA is only enabled once fencing works.** A heartbeat alone never decides who owns a VM. Planned: a hardware watchdog per node that resets the host without a valid quorum lease, optionally IPMI/Redfish fencing. A VM is started elsewhere only after the old node's lease has provably expired (self-fencing time plus safety margin).
- Live migration uses libvirt (`virDomainMigrate3`) with TLS between nodes; without shared storage via `VIR_MIGRATE_NON_SHARED_DISK`.

## Proxmox Backup Server integration (design)

The goal is to use existing PBS infrastructure and the PBS format instead of an own backup format.

- **Client**: `proxmox-backup-client` (official Debian package) for file and container backups (`pxar` archives) and the PBS API for datastore, snapshot, prune and GC information.
- **VM backups**: Proxmox VE backs up VM disks as a fixed index (`.fidx`) using its own QEMU extension (dirty bitmaps + PBS block driver in the Proxmox QEMU build). Debian's QEMU lacks that driver. Planned approach:
  1. Stage one: consistent snapshot (guest agent freeze if available) and upload of disks as image archives with `proxmox-backup-client backup disk.img:/dev/...`. Deduplicated and optionally encrypted by PBS, but **not incremental** at block level; the UI will say exactly that.
  2. Stage two: incremental backups via the libvirt backup API (`virDomainBackupBegin`, dirty bitmaps) uploading only changed chunks with the PBS protocol library (`libproxmox-backup-qemu0`).
- **Restoring existing Proxmox backups**: a Proxmox VM backup contains `qemu-server.conf` and `drive-*.img.fidx`. The compatibility adapter reads the config, translates it to libvirt XML (CPU, memory, disks, NICs, firmware) and restores the disks with `proxmox-backup-client restore`. Limits (UEFI variables, TPM state, passthrough devices, Proxmox-specific options) are shown before the restore starts.
- PBS snapshots are never pruned automatically without an explicit retention policy.

## Main technical risks

| Risk | Impact | Mitigation |
|---|---|---|
| PBS VM backups depend on Proxmox QEMU extensions | no incremental block backups with Debian QEMU | two-stage approach (above), honest labelling |
| wrong HA decisions (split brain) | data corruption from VMs started twice | HA only with watchdog fencing and quorum leases; real failure tests as release criterion |
| network changes cut off the host | remote management lost | changes only with preview, timeout and automatic rollback (planned); the installer never touches the network |
| root agent | compromise = root | minimal interface, input validation in the agent, socket only for group `hostmanaged`, no free-form commands |
| hosts without hardware virtualization (VPS) | VMs extremely slow | detected by installer and UI; software emulation only on explicit opt-in |
| SQLite with very many nodes | scaling limit | management server mode with PostgreSQL planned |

## Repository layout

```
cmd/hostmanaged      server, agent, admin and backup commands
cmd/hostctl          CLI
internal/agentapi    contract between control plane and agent, validation
internal/agent       agent server (UNIX socket)
internal/agentclient control plane client for the agent
internal/virt        libvirt adapter (VMs, storage, networks, statistics)
internal/lxc         LXC adapter
internal/hostinfo    host information from /proc and /sys
internal/api         REST API, sessions, CSRF, console WebSockets
internal/auth        password hashing, roles, rate limits
internal/jobs        job runner
internal/store       SQLite, migrations, audit hash chain
internal/config      configuration file
internal/webui       embedded web UI
web/                 frontend (React, TypeScript)
installer/           install.sh, uninstall.sh
deploy/systemd       systemd units
api/openapi.yaml     API description
docs/de, docs/en     documentation
```
