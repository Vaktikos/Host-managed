# Architektur

Dieses Dokument beschreibt die Architektur von Host-managed, die getroffenen Technologieentscheidungen, die Vertrauensgrenzen und die geplante Erweiterung zu Cluster, Hochverfügbarkeit und Proxmox-Backup-Server-Anbindung. Was davon heute umgesetzt ist, steht in [status.md](status.md).

## Überblick

```
 Browser / hostctl / Automatisierung
        │  HTTPS (TLS 1.2+), Sitzungs-Cookie + CSRF oder API-Token
        ▼
 ┌───────────────────────────────────────────┐
 │ hostmanaged server  (Benutzer hostmanaged) │  Control Plane
 │  REST-API /api/v1, Web-UI, RBAC, Audit,     │  – keine Root-Rechte
 │  Jobs, SQLite                               │  – systemd-Härtung
 └──────────────┬────────────────────────────┘
                │  UNIX-Socket /run/hostmanaged/agent.sock
                │  (0660 root:hostmanaged, SO_PEERCRED-Prüfung)
                │  typisierte Operationen, kein Shell-Zugriff
                ▼
 ┌───────────────────────────────────────────┐
 │ hostmanaged agent  (root)                   │  Node-Agent
 │  Adapter: virt (libvirt RPC), lxc, hostinfo │
 └───────┬───────────────────┬────────────────┘
         │ libvirt-sock       │ lxc-* (feste Argumente)
         ▼                    ▼
   libvirtd / QEMU / KVM     LXC (unprivilegiert, User-Namespaces)
```

Die Komponenten sind getrennt, auch wenn sie im Single-Node-Betrieb auf demselben Host laufen:

| Komponente | Aufgabe | Rechte | Code |
|---|---|---|---|
| Web-Frontend | React/TypeScript, Deutsch/Englisch | Browser | `web/` |
| Control Plane | API, Authentifizierung, RBAC, Jobs, Audit, Datenhaltung | unprivilegierter Benutzer | `internal/api`, `internal/auth`, `internal/jobs`, `internal/store` |
| Node-Agent | einziger Prozess mit Root-Rechten; führt nur definierte Operationen aus | root | `internal/agent` |
| Virtualisierungsadapter | KVM/QEMU über libvirt | im Agent | `internal/virt` |
| Container-Adapter | LXC | im Agent | `internal/lxc` |
| Host-Informationen | /proc, /sys | im Agent | `internal/hostinfo` |
| CLI | `hostctl` über die REST-API | API-Token | `cmd/hostctl` |

## Technologieentscheidungen

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Hostsystem | Debian 12/13 | Stabile Basis mit QEMU, libvirt, LXC, ZFS und Ceph aus den Paketquellen; dieselbe Grundlage wie Proxmox VE, dadurch gleiche Kernel- und Treiberverfügbarkeit. |
| Hypervisor | KVM/QEMU über libvirt | Bewährt, unterstützt Live-Migration, Snapshots, UEFI/OVMF, swtpm, PCI-Passthrough und Speicher-Pools. Eine eigene QEMU-Steuerung (wie `qm` bei Proxmox) wäre erst bei Funktionen nötig, die libvirt nicht abbildet. |
| libvirt-Anbindung | `github.com/digitalocean/go-libvirt` (reines Go, RPC-Protokoll) + `libvirt.org/go/libvirtxml` | Kein cgo, statische Binärdatei, Wiederverbindung nach libvirtd-Neustarts. |
| Container | LXC-Werkzeuge (`lxc-create`, `lxc-start` …) | Standard für Systemcontainer, unprivilegiert mit User-Namespaces. Aufruf ausschließlich mit festen Argumentlisten. |
| Backend und Agent | Go | Ein statisches Programm ohne Laufzeitabhängigkeiten, geringer Speicherbedarf, gute Nebenläufigkeit. |
| Frontend | React 18, TypeScript, Vite, i18next, noVNC, xterm.js | Verbreitet und gepflegt; eigene schlanke Komponenten statt großer UI-Bibliothek, damit das Design kontrolliert bleibt. |
| Datenhaltung (Single Node) | SQLite (WAL) über `modernc.org/sqlite` | Keine externe Datenbank auf einem einzelnen Host nötig. Schema-Migrationen versioniert. |
| Datenhaltung (Management-Server, geplant) | PostgreSQL | Für mehrere hundert Nodes und parallele Control-Plane-Instanzen. Der Store ist dafür als eigene Schicht gekapselt. |
| Jobs | eigener Runner mit persistentem Status in der Datenbank | Lang laufende Aktionen überleben die HTTP-Anfrage; nach einem Neustart werden unterbrochene Jobs ehrlich als „unterbrochen“ markiert statt als erfolgreich. |
| API | REST `/api/v1`, JSON, einheitliches Fehlerformat, OpenAPI in `api/openapi.yaml` | Leicht automatisierbar; Echtzeitdaten derzeit per Polling (3–5 s), SSE ist vorgesehen. |
| TLS | Selbstsigniertes Zertifikat (ECDSA P-256) bei der Installation, Fingerprint wird angezeigt | Sicherer Erststart ohne externe CA; eigenes Zertifikat jederzeit austauschbar. |

## Datenmodell

libvirt und LXC bleiben die Quelle der Wahrheit für Workloads. Host-managed speichert keine zweite Kopie der VM-Konfiguration, die auseinanderlaufen könnte:

- VM-Beschreibung, Tags, Notizen und Template-Markierung liegen im `<metadata>`-Element der libvirt-Domain (Namespace `https://host-managed.dev/xmlns/vm/1`).
- Container-Metadaten liegen in `hostmanaged.json` neben der LXC-Konfiguration.
- Die SQLite-Datenbank enthält nur Daten der Control Plane: Benutzer, Sitzungen, API-Tokens, Jobs mit Protokollen, Audit-Protokoll, Einstellungen.

## Vertrauensgrenzen

1. **Browser ↔ Control Plane**: HTTPS. Sitzungs-Cookie `HttpOnly`, `Secure`, `SameSite=Strict`; zusätzlich CSRF-Token im Header für alle schreibenden Anfragen. Strikte Content-Security-Policy. Berechtigungen werden ausschließlich serverseitig geprüft.
2. **Control Plane ↔ Agent**: UNIX-Socket mit Dateirechten 0660 (Gruppe `hostmanaged`) und zusätzlicher Prüfung der Prozess-UID per `SO_PEERCRED`. Der Agent akzeptiert nur die in `internal/agentapi` definierten Operationen und validiert jede Eingabe erneut (er vertraut der Control Plane nicht blind).
3. **Agent ↔ libvirt/LXC**: libvirt per RPC; LXC-Programme mit fester Argumentliste, nie über eine Shell. Namen, Distributionen, Bridges und Adressen werden vorher gegen strenge reguläre Ausdrücke bzw. `netip` geprüft.
4. **Konsolen**: VNC lauscht nicht auf TCP, sondern auf einem von libvirt verwalteten UNIX-Socket. Nur der Agent verbindet sich damit. Der Browser erreicht die Konsole über einen authentifizierten WebSocket (gleiche Herkunft erzwungen).
5. **Datenbank**: nur für den Benutzer `hostmanaged` lesbar. Passwörter als Argon2id-Hash, Sitzungs- und Token-Geheimnisse nur als SHA-256-Hash gespeichert. Das Audit-Protokoll ist per Trigger nur anhängbar und zusätzlich über eine Hash-Kette gegen Manipulation direkt in der Datei abgesichert.
6. **Backup-System (geplant)**: Zugangsdaten zum Proxmox Backup Server werden verschlüsselt gespeichert (Schlüssel außerhalb der Datenbank), mit minimal berechtigten API-Tokens.

## Multi-Node, Cluster und HA (Entwurf, noch nicht umgesetzt)

- Der Agent-Vertrag (`internal/agentapi`) ist transportunabhängig. Für entfernte Nodes wird derselbe Vertrag über gegenseitig authentifiziertes TLS (mTLS) angeboten; die Aufnahme eines Nodes erfolgt über ein einmaliges Beitrittstoken und den Abgleich des Zertifikat-Fingerprints.
- Cluster-Mitgliedschaft und Quorum werden nicht selbst erfunden: Geplant ist etcd (Raft) für Mitgliedschaft, Konfigurationsverteilung und Leases. Alternativ corosync/votequorum wie bei Proxmox.
- **HA wird erst freigegeben, wenn Fencing funktioniert.** Ein Heartbeat allein entscheidet nicht über den Eigentümer einer VM. Vorgesehen sind ein Hardware-Watchdog pro Node, der ohne gültige Quorum-Lease den Host zurücksetzt, sowie optional IPMI/Redfish-Fencing. Eine VM wird auf einem anderen Node erst gestartet, wenn die Lease des alten Nodes nachweislich abgelaufen ist (Self-Fencing-Zeit + Sicherheitsabstand).
- Live-Migration nutzt libvirt (`virDomainMigrate3`) mit TLS zwischen den Nodes; ohne gemeinsamen Storage per `VIR_MIGRATE_NON_SHARED_DISK`.

## Proxmox-Backup-Server-Anbindung (Entwurf)

Ziel ist, bestehende PBS-Infrastruktur und das PBS-Format zu nutzen statt eines eigenen Backup-Formats.

- **Client**: `proxmox-backup-client` (offizielles Paket, Debian) für Datei- und Container-Backups (`pxar`-Archive) sowie die PBS-API für Datastore-, Snapshot-, Prune- und GC-Informationen.
- **VM-Backups**: Proxmox VE sichert VM-Datenträger als „fixed index“ (`.fidx`) mit einer eigenen QEMU-Erweiterung (dirty bitmaps + PBS-Treiber im Proxmox-QEMU). Das Debian-QEMU besitzt diesen Treiber nicht. Geplanter Weg:
  1. Erste Stufe: konsistenter Snapshot (Guest-Agent-Freeze, falls verfügbar) und Hochladen der Datenträger als Image-Archiv mit `proxmox-backup-client backup disk.img:/dev/...`. Dedupliziert und verschlüsselbar durch PBS, aber **nicht inkrementell** auf Blockebene; wird in der Oberfläche genau so bezeichnet.
  2. Zweite Stufe: inkrementelle Sicherung über libvirt-Backup-API (`virDomainBackupBegin`, dirty bitmaps) und Übertragung nur geänderter Chunks über die PBS-Protokollbibliothek (`libproxmox-backup-qemu0`).
- **Wiederherstellung bestehender Proxmox-Backups**: Ein Proxmox-VM-Backup enthält `qemu-server.conf` und `drive-*.img.fidx`. Der Kompatibilitätsadapter liest die Konfiguration, übersetzt sie in libvirt-XML (CPU, RAM, Datenträger, Netzwerkkarten, Firmware) und stellt die Datenträger mit `proxmox-backup-client restore` bzw. `qmrestore`-gleichwertigen Schritten wieder her. Grenzen (UEFI-Variablen, TPM-Zustand, Passthrough-Geräte, Proxmox-spezifische Optionen) werden vor dem Restore angezeigt.
- Kein automatisches Löschen von PBS-Snapshots ohne ausdrückliche Prune-Richtlinie.

## Wichtigste technische Risiken

| Risiko | Auswirkung | Gegenmaßnahme |
|---|---|---|
| PBS-VM-Backups benötigen Proxmox-QEMU-Erweiterungen | Kein inkrementelles Blockbackup mit Debian-QEMU | Zweistufiger Ansatz (s. o.), ehrliche Kennzeichnung |
| Falsche HA-Entscheidungen (Split-Brain) | Datenkorruption durch doppelt gestartete VMs | HA erst mit Watchdog-Fencing und Quorum-Leases, reale Ausfalltests als Freigabekriterium |
| Netzwerkänderungen trennen den Host | Fernwartung nicht mehr möglich | Änderungen nur mit Vorschau, Zeitlimit und automatischem Rollback (geplant); Installer verändert das Netzwerk nicht |
| Agent mit Root-Rechten | Kompromittierung = Root | Minimale Schnittstelle, Eingabevalidierung im Agent, Socket nur für Gruppe `hostmanaged`, keine freien Befehle |
| Hosts ohne Hardware-Virtualisierung (vServer) | VMs extrem langsam | Erkennung im Installer und in der Oberfläche; Software-Emulation nur nach ausdrücklicher Zustimmung |
| SQLite bei sehr vielen Nodes | Skalierungsgrenze | Management-Server-Modus mit PostgreSQL vorgesehen |

## Repository-Struktur

```
cmd/hostmanaged     Server, Agent, Admin-, Backup-Befehle
cmd/hostctl         CLI
internal/agentapi   Vertrag zwischen Control Plane und Agent, Validierung
internal/agent      Agent-Server (UNIX-Socket)
internal/agentclient Client der Control Plane für den Agent
internal/virt       libvirt-Adapter (VMs, Storage, Netzwerke, Statistiken)
internal/lxc        LXC-Adapter
internal/hostinfo   Host-Informationen aus /proc und /sys
internal/api        REST-API, Sitzungen, CSRF, Konsolen-WebSockets
internal/auth       Passwort-Hashing, Rollen, Rate-Limits
internal/jobs       Job-Runner
internal/store      SQLite, Migrationen, Audit-Hash-Kette
internal/config     Konfigurationsdatei
internal/webui      eingebettete Weboberfläche
web/                Frontend (React, TypeScript)
installer/          install.sh, uninstall.sh
deploy/systemd      systemd-Units
api/openapi.yaml    API-Beschreibung
docs/de, docs/en    Dokumentation
```
