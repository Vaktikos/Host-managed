# Implementation status, tests and known limitations

State: milestone 1 (single node). "Implemented" means UI (where needed), API, backend logic, permission checks, error handling, tests and documentation exist. Everything else is marked *planned* or *not started* and is not offered as available in the UI.

## Feature overview

| Area | Feature | Status |
|---|---|---|
| Installation | installer for Debian 12/13 (checks, packages, pools, TLS, services, first admin, upgrade with backup) | implemented, tested on Debian 13 |
| | removal, control plane recovery | implemented (removal not tested) |
| | bootable ISO | not started |
| Sign-in | local users, Argon2id, sessions, CSRF, brute-force protection, password change, session list | implemented |
| | API tokens with expiry and revocation | implemented |
| | TOTP/MFA with QR code, recovery codes, reset by admin/CLI; secrets AES-GCM encrypted | implemented |
| | LDAP/AD, OIDC | planned |
| Permissions | 7 roles, server-side checks, last-admin protection | implemented |
| | per-resource permissions (ACL paths), custom roles | planned |
| Audit | log of all changes, sign-ins, denied requests; append-only; verifiable hash chain | implemented |
| VMs | create (UEFI/BIOS, Secure Boot, TPM, VirtIO/SATA/SCSI, ISO, NAT/bridge, guest agent channel) | implemented |
| | start, shut down, power off, reboot, reset, pause/resume | implemented |
| | delete with/without disks, lock against concurrent operations | implemented |
| | change CPU/memory, description, tags, notes, autostart, template flag | implemented |
| | VNC console in the browser | implemented |
| | live statistics (CPU, balloon memory, network, disk), guest IPs via guest agent | implemented |
| | add/remove/grow disks, add/remove NICs, change ISO, boot order; hotplug where QEMU supports it | implemented |
| | cloning (full; linked from templates), cloud-init (NoCloud), VMs from cloud images | implemented |
| | snapshots (internal, with RAM for running BIOS VMs; running UEFI VMs not possible – QEMU limit), revert, delete | implemented |
| | serial console, SPICE | planned |
| | PCI/GPU passthrough, SR-IOV, NUMA | planned |
| | start order, start delay, dependencies | planned (autostart only) |
| Containers | create from official LXC images, unprivileged, limits, static IPv4/DHCP | implemented |
| | start, shut down, stop, reboot, freeze, delete | implemented |
| | browser console | implemented |
| | IPv6 configuration, bind mounts, disk quotas, cloning | planned |
| Storage | list pools and volumes, ISO upload, download from URL (SSRF protection, checksum), delete volumes (with usage check) | implemented |
| | create/remove pools (LVM thin, ZFS, NFS, iSCSI, Ceph RBD) from the UI | planned |
| | grow/move disks, SMART | planned |
| Network | list interfaces, bridges, bonds, VLANs and libvirt networks | implemented |
| | changes with preview and automatic rollback | planned |
| | firewall (nftables) | planned |
| Jobs | persistent jobs, log, measured progress, cancellation, interrupted jobs marked | implemented |
| Backups | control plane backup (`hostmanaged backup`) | implemented |
| | local VM/CT backups to backup stores (directory/NFS pools `backup`, `*-backup`): running VMs point-in-time via libvirt backup job (application consistent with guest agent), offline copy; containers stop/suspend/live | implemented |
| | restore (new name or disaster recovery with original UUID/MAC), protection, retention (PBS semantics), schedules with weekdays/time, run now | implemented |
| | incremental/deduplicated backups, encryption, file-level restore, verify jobs | planned |
| | Proxmox Backup Server (connection, VM/CT backups, schedules, restore, import of Proxmox backups) | planned, design in [architecture.md](architecture.md) |
| Cluster | node agent over mTLS, cluster, migration, HA with fencing | planned, design in [architecture.md](architecture.md) |
| Tenancy | tenants, projects, quotas, reseller API, webhooks | planned |
| Monitoring | notifications (e-mail, webhook), thresholds, long-term metrics | planned |
| UI | German/English, resource tree, global search, tables with sort/filter/column selection, task bar, responsive | implemented |
| CLI | `hostctl` for node, VMs, containers, storage, backups, jobs | implemented |
| API | REST v1, OpenAPI description, uniform errors, job IDs | implemented |
| | SSE/WebSocket for state changes, webhooks, idempotency keys | planned (status by polling) |

## Tests

| Test | Environment | Result |
|---|---|---|
| Go unit tests (`make test-go`, with `-race`) | store, auth, jobs, API (with a test double for the agent), LXC (argument checks), host info, configuration | pass |
| Frontend (`make test-web`) | TypeScript check; translations: same keys in DE/EN, all used keys present, identical placeholders | pass |
| libvirt integration (`HM_LIBVIRT_INTEGRATION=1`) | real libvirtd 10.0 + QEMU 8.2, software emulation (no KVM in the test environment): create, start, VNC socket (RFB greeting), statistics, pause, resources, metadata, template, disk protection, delete | pass |
| LXC integration (`HM_LXC_INTEGRATION=1`) | real LXC 5.0: create Alpine container, start, verify user namespace, stop, delete | pass |
| Browser end-to-end (Playwright) | sign-in, language switch, create VM via dialog, create container, use container console, VNC console | pass |
| Cloud-init (`HM_CLOUD_IMAGE`) | Debian 13 cloud image under real libvirt: import, grow, seed, boot, DHCP, SSH login with key, host name, sudo; clone with own seed | pass |
| Hardware/snapshots/clones | real libvirt: disk/NIC hotplug, SATA fallback, online grow, ISO, boot order, RAM snapshot + revert, full/linked clone, template base protection | pass |
| Backups (libvirt/LXC integration) | running VM push backup, offline backup, restore with new identity and with kept UUID/MAC, refusal on name conflict; container in stop/suspend/live mode, restore under new name with ownership (shifted UIDs) and config rewrite | pass |
| Backups end-to-end | CLI and browser: backup, restore + start, protection, prune preview, schedule create and run now (with pruning) | pass |
| MFA in the browser | setup with QR, wrong code, login with TOTP, disable with recovery code | pass |
| Installer | Debian 13 cloud image (systemd) in QEMU: first install, upgrade with backup, service restart, VM with UEFI + Secure Boot + TPM via nested KVM, Debian container with DHCP; build from source with verified Go archive | pass |
| Installer on Debian 12 | – | **not tested** |
| Installer on real hardware | – | **not tested** |
| Load tests, large inventories | – | **not done** |

Failure cases with tests: invalid/revoked/tampered API token, missing permission, missing/wrong CSRF token, brute-force lockout, full storage (job fails with the real message), agent unreachable (503), duplicate VM name, deleting running VMs/containers, deleting volumes in use, audit log tampering, database newer than binary, cancelled and panicking jobs, parallel job limit.

## Known limitations

- Single node: control plane and agent run on the same host.
- Live status by polling (3–5 s), not push.
- Job logs are technical messages in English.
- API validation messages are English; the UI validates input itself first and shows translated messages.
- A static container IP is set in the LXC config; a distribution with an active DHCP client additionally obtains a DHCP address.
- The installer does not create a bridge (see [operations.md](operations.md#setting-up-a-bridge)).
- Backups are full copies (qcow2 per disk, tar.gz per container); no deduplication or incremental backups yet. A backup store on the same disk as the VMs does not protect against disk failure.
- Running UEFI VMs: NVRAM is copied while running and may be slightly older than the disk state.
- A container restored under a new name keeps the old host name inside the guest (`/etc/hostname` is not changed from the host for safety).
- Schedules run in the server time zone; a slot missed by more than an hour is skipped (audit entry `backup.schedule.missed`).
- Without hardware virtualization (many VPS), VMs only run with software emulation.
