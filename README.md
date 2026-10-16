# Host-managed

Virtualisierungsplattform für Debian-Server: KVM/QEMU-VMs und LXC-Container über eine deutsch-englische Weboberfläche, eine REST-API und das CLI `hostctl` verwalten. Ziel ist eine eigenständige Alternative zu Proxmox VE. Dieses Repository enthält den ersten Meilenstein: einen **voll nutzbaren Single-Node-Host**.

*English summary below.*

## Was heute funktioniert

- **Installation** auf Debian 12/13 mit einem Befehl; prüft Hardware, installiert QEMU/KVM, libvirt, OVMF, swtpm und LXC, richtet TLS, Dienste und den ersten Administrator ein. Netzwerk und Datenträger werden nicht verändert. Update mit vorheriger Sicherung.
- **Virtuelle Maschinen**: erstellen (UEFI/BIOS, Secure Boot, TPM 2.0, VirtIO/SCSI/SATA, ISO, NAT oder Bridge), starten, herunterfahren, ausschalten, neu starten, zurücksetzen, pausieren, löschen; CPU/RAM ändern; Beschreibung, Tags, Notizen, Autostart, Template-Markierung; **VNC-Konsole im Browser**; Live-Statistiken; Gast-IPs über den QEMU Guest Agent.
- **Container**: unprivilegierte LXC-Container aus den offiziellen Abbildern, Limits für RAM/Swap/CPU, statische IPv4 oder DHCP, Lebenszyklus, **Terminal im Browser**.
- **Storage**: libvirt-Pools und Volumes, ISO-Upload, Schutz benutzter Volumes.
- **Netzwerk**: Host-Schnittstellen, Bridges, Bonds, VLANs und libvirt-Netze (Anzeige).
- **Sicherheit**: Rollen und Berechtigungen, Argon2id, Sitzungen mit CSRF-Schutz, API-Tokens, Brute-Force-Schutz, manipulationsgeschütztes Audit-Protokoll, Agent mit Root-Rechten getrennt von der unprivilegierten Weboberfläche.
- **Aufgaben** mit Protokoll, **Sicherung der Verwaltungsdaten**, **CLI** `hostctl`, **REST-API** mit OpenAPI-Beschreibung.

Was noch fehlt (u. a. Proxmox-Backup-Server, Snapshots, Klonen, Cluster, HA, Netzwerkänderungen, MFA), steht ehrlich in [docs/de/status.md](docs/de/status.md). Die Oberfläche bietet nichts an, was nicht funktioniert.

## Installation

```bash
apt-get update && apt-get install -y git
git clone https://github.com/Vaktikos/Host-managed.git
cd Host-managed
./installer/install.sh
```

Danach `https://<server-ip>:8443` öffnen. Details, Update, Deinstallation, Wiederherstellung: [docs/de/installation.md](docs/de/installation.md).

## Dokumentation

| | Deutsch | English |
|---|---|---|
| Architektur, Technologieentscheidungen, PBS- und Cluster-Konzept | [architektur.md](docs/de/architektur.md) | [architecture.md](docs/en/architecture.md) |
| Installation, Update, Recovery, Fehlersuche | [installation.md](docs/de/installation.md) | [installation.md](docs/en/installation.md) |
| Betrieb: VMs, Container, Storage, Netzwerk, Benutzer, CLI, API | [betrieb.md](docs/de/betrieb.md) | [operations.md](docs/en/operations.md) |
| Sicherheitskonzept, Berechtigungen | [sicherheit.md](docs/de/sicherheit.md) | [security.md](docs/en/security.md) |
| Umsetzungsstand, Tests, Einschränkungen | [status.md](docs/de/status.md) | [status.md](docs/en/status.md) |
| Entwicklung, Release-Prozess | [entwicklung.md](docs/de/entwicklung.md) | [development.md](docs/en/development.md) |
| REST-API | [api/openapi.yaml](api/openapi.yaml) | |

## Entwicklung

```bash
make build                    # Frontend + bin/hostmanaged + bin/hostctl
make test                     # Unit-Tests, Typprüfung, Übersetzungstests
sudo make test-integration    # gegen echtes libvirt/QEMU und LXC
```

---

## English summary

Host-managed is a virtualization platform for Debian servers: manage KVM/QEMU VMs and LXC containers through a German/English web UI, a REST API and the `hostctl` CLI. It aims to become a self-contained alternative to Proxmox VE. This repository contains the first milestone, a **fully usable single-node host**: one-command installer for Debian 12/13, VM lifecycle with UEFI/Secure Boot/TPM and a browser VNC console, unprivileged LXC containers with a browser terminal, storage pools with ISO upload, read-only network view, roles and permissions, API tokens, a tamper-evident audit log, persistent jobs, control plane backups, CLI and an OpenAPI-described REST API.

Install: see the commands above, then open `https://<server-ip>:8443`. What is implemented, tested and still missing is listed in [docs/en/status.md](docs/en/status.md).
