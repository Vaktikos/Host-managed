# Installation, upgrade, removal and recovery

## Requirements

| | Minimum | Recommended |
|---|---|---|
| Operating system | Debian 12 (bookworm) or Debian 13 (trixie), fresh install | Debian 13 |
| Architecture | amd64 (arm64 untested) | amd64 |
| CPU | Intel VT-x or AMD-V enabled in BIOS/UEFI | with IOMMU (VT-d/AMD-Vi) for passthrough |
| Memory | 2 GiB + what the VMs need | 16 GiB or more |
| Storage | 10 GiB free under `/var/lib` | dedicated SSD/NVMe for VM disks |
| Network | internet access for packages and container images | |

Rented VPS often lack hardware virtualization. The installer detects that (`/dev/kvm` missing) and stops. With `--allow-no-kvm`, VMs use software emulation, which is only usable for testing.

## Installation

As root on the server:

```bash
apt-get update && apt-get install -y git
git clone https://github.com/Vaktikos/Host-managed.git
cd Host-managed
./installer/install.sh
```

Without prebuilt binaries the installer builds from source. It downloads Go 1.26.0 from `dl.google.com` (checksum pinned in the installer) and installs Node.js from the Debian archive.

With prebuilt binaries (e.g. from `make release`):

```bash
tar xzf host-managed-<version>-linux-amd64.tar.gz
cd host-managed-<version>
./installer/install.sh --binary-dir bin
```

### Options

| Option | Meaning |
|---|---|
| `--port 8443` | HTTPS port of the web interface |
| `--admin admin` | name of the first administrator |
| `--allow-no-kvm` | continue without `/dev/kvm` (software emulation) |
| `--skip-packages` | do not install Debian packages |
| `--binary-dir DIR` | use prebuilt binaries |
| `--yes` | do not ask |
| `--lang de\|en` | installer language and default UI language |

### What the installer does

1. Checks OS, architecture, CPU virtualization, boot mode, memory, free space, management address and whether the port is free.
2. Installs QEMU/KVM, libvirt, OVMF, swtpm, LXC and helpers (`--no-install-recommends`).
3. Creates the system user `hostmanaged`, `/etc/hostmanaged`, `/var/lib/hostmanaged` (database) and `/var/lib/hostmanaged-storage` (VM disks, ISO images).
4. Defines the libvirt storage pools `local` and `iso` and starts the libvirt NAT network `default` (192.168.122.0/24) unless it collides with the host network.
5. Adds `root:100000:65536` to `/etc/subuid` and `/etc/subgid` (unprivileged containers) if there is no entry yet.
6. Creates a self-signed TLS certificate (ECDSA P-256, all host addresses as SAN) and prints its SHA-256 fingerprint.
7. Installs `/usr/local/sbin/hostmanaged`, `/usr/local/bin/hostctl` and the systemd units; starts `hostmanaged-agent` and `hostmanaged`.
8. On first install creates an administrator with a random password. The password is shown on the terminal and stored in `/root/hostmanaged-initial-admin.txt` (root only), never in the install log.

The installer **does not change network interfaces, bridges, routes or firewall rules and never formats disks.** If a firewall with a default `drop` policy is active, it tells you to open the port.

Log: `/var/log/hostmanaged-install.log`

### First login

1. Open `https://<server-ip>:8443`.
2. The browser warns about the self-signed certificate. Compare the fingerprint it shows with the one printed by the installer and only continue if they match.
3. Sign in as `admin` with the printed password, set a new password under **Account**, and delete `/root/hostmanaged-initial-admin.txt`.

Own certificate (e.g. Let's Encrypt): copy certificate and key to `/etc/hostmanaged/tls/server.crt` and `server.key` (key `0640 root:hostmanaged`) and run `systemctl restart hostmanaged`.

### Bridge for VMs on the host network

VMs use the NAT network `default` by default. For VMs with their own public addresses you need a Linux bridge (e.g. `vmbr0`). The installer deliberately does **not** create it, because a faulty change cuts off remote access. See [operations.md](operations.md#setting-up-a-bridge) for the procedure with console access (provider KVM/IPMI/rescue system). Many hosting providers also require additional IPs to be bound to a virtual MAC or routed.

## Upgrade

```bash
cd Host-managed
git pull
./installer/install.sh
```

During an upgrade the installer first backs up the database and configuration (`/var/lib/hostmanaged/backups/`), stops control plane and agent, replaces the binaries and starts both services. Configuration, certificate and data are kept. Database migrations run automatically on start; an older binary refuses to start with a newer database.

**Running VMs and containers are not stopped by an upgrade**: they belong to libvirt and LXC. While the services restart (a few seconds) the web UI and API are unavailable, open consoles disconnect and running jobs are marked "interrupted".

Rollback: check out the previous version and run `./installer/install.sh` again. If the database schema changed, also restore the backup from `/var/lib/hostmanaged/backups/` (see below).

## Removal

```bash
./installer/uninstall.sh                 # remove services and binaries
./installer/uninstall.sh --purge         # also configuration and database (final backup to /root first)
./installer/uninstall.sh --purge-images  # also all VM disks and ISOs (asks for confirmation)
```

VMs and containers keep running after removal and remain manageable with `virsh` and `lxc-*`.

## Backing up control plane data

```bash
hostmanaged backup            # → /var/lib/hostmanaged/backups/hostmanaged-<time>.tar.gz
hostmanaged backup -output /root/hm.tar.gz
```

The archive contains a consistent database snapshot (users, roles, tokens, jobs, audit log) and `/etc/hostmanaged` including the TLS key. It is created with mode `0600` and must be treated as a secret. VM disks are **not** included; they are protected by VM backups (Proxmox Backup Server integration planned, see [status.md](status.md)).

## Restoring the control plane

1. Reinstall Debian and run `./installer/install.sh`.
2. `systemctl stop hostmanaged hostmanaged-agent`
3. Extract and copy back:
   ```bash
   mkdir /tmp/r && tar -C /tmp/r -xzf hostmanaged-<time>.tar.gz
   install -m 0640 -o hostmanaged -g hostmanaged /tmp/r/data/hostmanaged.db /var/lib/hostmanaged/hostmanaged.db
   rm -f /var/lib/hostmanaged/hostmanaged.db-wal /var/lib/hostmanaged/hostmanaged.db-shm
   cp -a /tmp/r/config/. /etc/hostmanaged/
   chown -R root:hostmanaged /etc/hostmanaged && chmod 0640 /etc/hostmanaged/tls/server.key
   ```
4. `systemctl start hostmanaged-agent hostmanaged`
5. VMs and containers are read from libvirt/LXC; if their disks are still on the host they appear immediately. After a reinstall, the libvirt definitions (`/etc/libvirt/qemu/*.xml`) and `/var/lib/lxc` must be restored from a file backup.

Forgotten admin password: `hostmanaged admin reset-password --username admin` (as root; signs out all of the user's sessions).

## Troubleshooting

| Symptom | Check |
|---|---|
| web UI unreachable | `systemctl status hostmanaged`, `journalctl -u hostmanaged -n 50`, host/provider firewall for the port |
| "node agent not reachable" | `systemctl status hostmanaged-agent`, `ls -l /run/hostmanaged/agent.sock` (group `hostmanaged`) |
| VM does not start: "Permission denied" on disk | the disk must be reachable for `libvirt-qemu`; do not put pools under `/root` or `0700` directories |
| "/dev/kvm is not available" | enable VT-x/AMD-V in firmware; `lsmod \| grep kvm`; ask your VPS provider for nested virtualization |
| NAT network `default` does not start | 192.168.122.0/24 already in use: `virsh net-edit default` and choose another range |
| container without IP | bridge present? `virsh net-list`; inside the container `ip a`; distribution DHCP client |
| container creation fails | internet access to `images.linuxcontainers.org`; `/etc/subuid` contains `root:100000:65536` |
| too many failed logins | wait 15 minutes or restart the service |
