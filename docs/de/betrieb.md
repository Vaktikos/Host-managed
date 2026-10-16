# Betriebshandbuch

Beschreibt die Bedienung der Version, die heute im Repository steht. Geplante Funktionen sind in [status.md](status.md) aufgeführt.

## Virtuelle Maschinen

**Erstellen** (Weboberfläche: *Virtuelle Maschinen → VM erstellen*, CLI: `hostctl vm create`):

| Einstellung | Hinweise |
|---|---|
| Name | 1–63 Zeichen, Buchstaben, Ziffern, `.`, `_`, `-`; eindeutig |
| vCPUs, Arbeitsspeicher | änderbar nach dem Erstellen (wirksam beim nächsten Start) |
| CPU-Modus | `host-passthrough` (Standard, beste Leistung), `host-model` (besser für Migration), `qemu64` (maximale Kompatibilität) |
| Firmware | UEFI (OVMF, Standard) oder BIOS (SeaBIOS); Secure Boot nur mit UEFI und KVM |
| TPM 2.0 | emuliert durch swtpm (z. B. für Windows 11) |
| Datenträger | Größe in GiB, Pool, Bus VirtIO-Block (Standard), VirtIO-SCSI oder SATA. In Verzeichnis-Pools als qcow2 mit Thin Provisioning |
| Installationsmedium | ISO aus einem Pool mit Inhalt „ISO“ (Pool `iso`; Upload unter *Storage*) |
| Netzwerk | libvirt-Netzwerk (z. B. `default`, NAT) oder Linux-Bridge des Hosts |
| Guest-Agent-Kanal | Kanal für `qemu-guest-agent`; nötig für Gast-IP-Adressen und sauberes Herunterfahren |

Boot-Reihenfolge: Festplatte vor CD-ROM. Eine leere Festplatte wird übersprungen, das Installationsmedium bootet. Nach der Installation startet die VM von der Festplatte, ohne dass das ISO entfernt werden muss.

**Energieverwaltung**: Starten, Herunterfahren (ACPI), Neu starten, Pausieren/Fortsetzen, Ausschalten (sofort, mit Bestätigung), Hart zurücksetzen (mit Bestätigung). Jede Aktion wird im Audit-Protokoll vermerkt.

**Konsole**: VNC im Browser (noVNC) mit Strg+Alt+Entf und Skalierung. Die Verbindung läuft über die angemeldete Sitzung; VNC ist auf dem Host nicht per TCP erreichbar.

**Übersicht**: CPU-, Speicher-, Netzwerk- und Datenträgerdurchsatz der letzten 10 Minuten (Messung alle 5 s durch den Agent). Speicherbelegung im Gast nur, wenn der Balloon-Treiber Werte liefert. Gast-IP-Adressen nur, wenn der QEMU Guest Agent im Gast läuft; andernfalls wird „antwortet nicht“ angezeigt, nichts geschätzt.

**Notizen und Optionen**: Beschreibung, Tags, Notizen, Autostart. Als Template markierte VMs können nicht gestartet werden.

**Löschen**: nur im ausgeschalteten Zustand, Bestätigung durch Eingabe des Namens. Optional werden die Datenträger der VM gelöscht (nur Volumes in libvirt-Pools; ISO-Medien nie).

Nicht möglich in dieser Version: Datenträger/Netzwerkkarten nachträglich hinzufügen oder ändern, Snapshots, Klonen, Cloud-Init, PCI-Passthrough, Migration. Diese Funktionen stehen in [status.md](status.md).

## Container (LXC)

**Erstellen**: Abbild (Distribution/Version) aus der offiziellen Liste von `images.linuxcontainers.org`, RAM-, Swap- und CPU-Limit, Bridge (z. B. `virbr0` für das NAT-Netz), statische IPv4-Adresse mit Gateway oder DHCP.

Container sind immer **unprivilegiert**: root im Container entspricht UID 100000 auf dem Host (User-Namespace), AppArmor-Profil `generated`, Verschachtelung deaktiviert.

**Konsole**: Terminal im Browser; öffnet eine Root-Anmeldung im Container (`lxc-attach … /bin/login -f root`). Das Recht `ct.console` ist daher gleichbedeutend mit Root-Zugriff im Container.

Limits und Autostart können unter *Übersicht → Bearbeiten* geändert werden; sie gelten ab dem nächsten Start.

## Storage

Angezeigt werden alle libvirt-Storage-Pools mit Typ, Zustand, Kapazität und Belegung. Pools mit dem Namen `iso` (oder Endung `-iso`) gelten als ISO-Ablage, alle anderen als Ablage für VM-Datenträger. In Verzeichnis-Pools können Dateien hochgeladen werden (Standardlimit 64 GiB, `max_upload_gib`). Volumes, die eine VM verwendet, lassen sich nicht löschen.

Weitere Pools (LVM-Thin, ZFS, NFS …) können derzeit mit `virsh pool-define` angelegt werden und erscheinen dann automatisch. VM-Datenträger werden in Pools vom Typ `dir`, `fs`, `netfs` (qcow2) sowie `logical`, `zfs`, `rbd` (raw) angelegt; nur `dir` ist getestet.

## Netzwerk

Angezeigt werden Host-Schnittstellen (Art, Adressen, Bridge-Mitglieder, MTU, Geschwindigkeit, Zähler) und libvirt-Netzwerke. Änderungen erfolgen in dieser Version außerhalb der Oberfläche.

### Bridge einrichten

Nur mit Konsolenzugang zum Server (Rettungssystem, KVM/IPMI des Anbieters), da eine Fehlkonfiguration die Verbindung trennt. Beispiel für Debian mit `ifupdown` (`/etc/network/interfaces`), physische Karte `eno1`, Adresse bisher direkt auf `eno1`:

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

Danach `apt install bridge-utils` (falls nötig) und `systemctl restart networking` bzw. Neustart. Die Bridge erscheint im VM-Dialog als „Bridge vmbr0“. Viele Anbieter (z. B. Hetzner) verlangen für zusätzliche IPs eine eigene MAC-Adresse aus dem Kundenportal oder ein geroutetes Setup.

## Backups

Backup-Speicher sind libvirt-Verzeichnis- oder NFS-Pools namens `backup` oder mit der Endung `-backup`; das Installationsskript legt `backup` in `/var/lib/hostmanaged-storage/backup` an. Für echten Schutz gehört ein Speicher auf eine separate Platte oder eine NFS-Freigabe, z. B.:

```bash
virsh pool-define-as nas-backup netfs --source-host nas.example --source-path /export/backup --target /mnt/nas-backup
virsh pool-build nas-backup && virsh pool-start nas-backup && virsh pool-autostart nas-backup
```

Aufbau: `<Speicher>/<vm|ct>/<Name>/<Zeitstempel>/` mit `manifest.json`, `domain.xml`, `disk-<Ziel>.qcow2`, `nvram.fd` (VMs) bzw. `container.tar.gz` (Container). Backups werden nach `<Zeitstempel>.partial` geschrieben und erst vollständig sichtbar; Reste nach einem Absturz entfernt der Agent beim Start.

| Gast | Modus | Konsistenz |
|---|---|---|
| VM läuft | libvirt-Push-Backup-Job (zeitpunktgenau); Dateisysteme werden über den QEMU-Gast-Agent eingefroren, wenn er antwortet | Anwendung, sonst Absturz |
| VM aus | Kopie mit `qemu-img` | offline |
| Container `stop` (Standard) | herunterfahren, archivieren, wieder starten | offline |
| Container `suspend` | während des Archivierens eingefroren | Absturz |
| Container `live` | im laufenden Betrieb archiviert | keine – nur wenn Inkonsistenz hinnehmbar ist |

**Wiederherstellung** legt eine neue VM bzw. einen neuen Container an. Unter dem ursprünglichen Namen – und nur wenn das Original nicht mehr existiert – bleiben UUID und MAC-Adressen erhalten (Desaster-Recovery); sonst bekommt der Gast eine neue Identität. Es wird nie etwas überschrieben.

**Aufbewahrung** (`keep_last`, `keep_hourly`, `keep_daily`, `keep_weekly`, `keep_monthly`, `keep_yearly`) folgt den Regeln des Proxmox Backup Servers. Geschützte Backups werden nie entfernt oder gelöscht.

**Zeitpläne** (*Backups → Zeitpläne*): Wochentage und Uhrzeit (Zeitzone des Servers), Gäste (alle, alle VMs, alle Container oder Auswahl), Speicher, Container-Modus, Aufbewahrung. Aufgeräumt wird pro Gast nach seiner Sicherung. Fehlgeschlagene Gäste halten die übrigen nicht auf; die Aufgabe schlägt fehl und nennt sie.

Rollen: `backup-admin` darf alles rund um Backups; `vm-admin` darf sichern und wiederherstellen; `operator` darf sichern; `auditor` darf ansehen.

```bash
hostctl backup create vm web1 --notes "vor dem Update" --wait
hostctl backup create ct dns1 --mode suspend --wait
hostctl backup list --vm web1
hostctl backup restore backup vm/web1/2026-10-16T01-37-36Z --name web1-test --pool local --start --wait
hostctl backup protect backup vm/web1/2026-10-16T01-37-36Z
hostctl backup prune vm web1 --keep-daily 7 --keep-weekly 4 --dry-run
hostctl backup schedule list
```

## Benutzer und Rollen

| Rolle | Rechte |
|---|---|
| Administrator (`admin`) | alles |
| VM-Administrator (`vm-admin`) | VMs und Container: anzeigen, steuern, Konsole, konfigurieren, erstellen, löschen, Snapshots; ISO hochladen; Backups erstellen und wiederherstellen |
| Operator (`operator`) | VMs und Container anzeigen, steuern, Konsole; Backups erstellen |
| Storage-Administrator (`storage-admin`) | Storage anzeigen, hochladen, löschen |
| Netzwerk-Administrator (`network-admin`) | Netzwerk anzeigen |
| Backup-Administrator (`backup-admin`) | VMs/Container anzeigen; Backups erstellen, wiederherstellen, schützen, aufräumen, löschen; Zeitpläne verwalten |
| Nur Lesen (`auditor`) | alles anzeigen, Audit-Protokoll lesen, nichts ändern |

Rollen gelten für den ganzen Node. Die Übersicht der einzelnen Berechtigungen steht in der Oberfläche unter *Benutzer → Rollen*. Der letzte aktive Administrator kann weder gelöscht noch herabgestuft werden.

Passwörter: mindestens 12 Zeichen; Argon2id. Nach 8 Fehlversuchen pro Benutzer bzw. 20 pro IP-Adresse wird die Anmeldung 15 Minuten gesperrt. Sitzungen laufen nach 2 Stunden Inaktivität bzw. spätestens nach 12 Stunden ab (konfigurierbar).

## Aufgaben und Audit

Lang laufende Aktionen (VM/Container erstellen und löschen) laufen als Aufgabe mit Protokoll. Ein Prozentwert erscheint nur, wo er gemessen wird (Downloads, Backups, Wiederherstellungen, Plattenkopien); sonst zeigt die Oberfläche „Wird ausgeführt“. Aufgaben, die bei einem Dienstneustart liefen, werden als „unterbrochen“ markiert. Aufgaben älter als 90 Tage werden entfernt.

Das Audit-Protokoll enthält Benutzer, Zeit, Quell-IP, Aktion, Ressource, Ergebnis, Korrelations-ID und Details aller Anmeldungen, Änderungen und verweigerten Zugriffe. *Integrität prüfen* kontrolliert die Hash-Kette.

## Kommandozeile (hostctl)

API-Token unter *Konto → API-Tokens* erzeugen, dann:

```bash
export HM_URL=https://server:8443
export HM_TOKEN=hm_...
export HM_FINGERPRINT=0F:26:...     # Fingerprint aus dem Installer (selbstsigniertes Zertifikat)

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

Die Hilfe erscheint auf Deutsch, wenn `LANG` mit `de` beginnt.

## REST-API

Beschreibung: [`api/openapi.yaml`](../../api/openapi.yaml). Authentifizierung per `Authorization: Bearer hm_…` (API-Token) oder Sitzungs-Cookie plus `X-CSRF-Token`. Fehler haben immer die Form:

```json
{"error": {"code": "validation", "message": "…", "field": "name", "request_id": "…"}}
```

Lang laufende Operationen antworten mit `202 Accepted` und einem Job-Objekt; Status unter `GET /api/v1/jobs/{id}`, Protokoll unter `GET /api/v1/jobs/{id}/log?after=<id>`.
