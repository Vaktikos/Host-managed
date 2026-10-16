# Installation, Update, Deinstallation und Wiederherstellung

## Voraussetzungen

| | Mindestens | Empfohlen |
|---|---|---|
| Betriebssystem | Debian 12 (bookworm) oder Debian 13 (trixie), frisch installiert | Debian 13 |
| Architektur | amd64 (arm64 ungetestet) | amd64 |
| CPU | Intel VT-x oder AMD-V, im BIOS/UEFI aktiviert | mit IOMMU (VT-d/AMD-Vi) für Passthrough |
| RAM | 2 GiB + Bedarf der VMs | 16 GiB und mehr |
| Speicher | 10 GiB frei unter `/var/lib` | eigene SSD/NVMe für VM-Datenträger |
| Netzwerk | Internetzugang für Pakete und Container-Abbilder | |

Auf einem gemieteten vServer ist Hardware-Virtualisierung oft nicht verfügbar. Der Installer erkennt das (`/dev/kvm` fehlt) und bricht ab. Mit `--allow-no-kvm` laufen VMs per Software-Emulation, das ist aber nur zum Testen brauchbar.

## Installation

Als root auf dem Server:

```bash
apt-get update && apt-get install -y git
git clone https://github.com/Vaktikos/Host-managed.git
cd Host-managed
./installer/install.sh
```

Ohne fertige Binärdateien baut der Installer das Programm aus dem Quellcode. Dafür lädt er Go 1.26.0 von `dl.google.com` (Prüfsumme fest im Installer hinterlegt) und installiert Node.js aus den Debian-Paketquellen.

Mit fertigen Binärdateien (z. B. aus `make release`):

```bash
tar xzf host-managed-<version>-linux-amd64.tar.gz
cd host-managed-<version>
./installer/install.sh --binary-dir bin
```

### Optionen

| Option | Bedeutung |
|---|---|
| `--port 8443` | HTTPS-Port der Weboberfläche |
| `--admin admin` | Name des ersten Administrators |
| `--allow-no-kvm` | ohne `/dev/kvm` fortfahren (Software-Emulation) |
| `--skip-packages` | keine Debian-Pakete installieren |
| `--binary-dir DIR` | fertige Binärdateien verwenden |
| `--yes` | ohne Rückfrage |
| `--lang de\|en` | Sprache des Installers und Standardsprache der Oberfläche |

### Was der Installer tut

1. Prüft System, Architektur, CPU-Virtualisierung, Boot-Modus, RAM, freien Speicher, Management-Adresse und ob der Port frei ist.
2. Installiert QEMU/KVM, libvirt, OVMF, swtpm, LXC und Hilfsprogramme (`--no-install-recommends`).
3. Legt den Systembenutzer `hostmanaged`, `/etc/hostmanaged`, `/var/lib/hostmanaged` (Datenbank) und `/var/lib/hostmanaged-storage` (VM-Datenträger, ISO-Abbilder) an.
4. Richtet die libvirt-Storage-Pools `local` und `iso` ein und aktiviert das libvirt-NAT-Netzwerk `default` (192.168.122.0/24), falls es nicht mit dem Hostnetz kollidiert.
5. Trägt `root:100000:65536` in `/etc/subuid` und `/etc/subgid` ein (unprivilegierte Container), falls noch kein Eintrag existiert.
6. Erzeugt ein selbstsigniertes TLS-Zertifikat (ECDSA P-256, alle Host-Adressen als SAN) und zeigt den SHA-256-Fingerprint an.
7. Installiert `/usr/local/sbin/hostmanaged`, `/usr/local/bin/hostctl` und die systemd-Units, startet `hostmanaged-agent` und `hostmanaged`.
8. Legt beim ersten Mal einen Administrator mit zufälligem Passwort an. Das Passwort erscheint nur im Terminal und in `/root/hostmanaged-initial-admin.txt` (nur root lesbar), nicht im Installationsprotokoll.

Der Installer **ändert keine Netzwerkschnittstellen, Bridges, Routen oder Firewall-Regeln und formatiert keine Datenträger.** Ist eine Firewall mit Standard-Policy `drop` aktiv, weist er darauf hin, dass der Port freigegeben werden muss.

Protokoll: `/var/log/hostmanaged-install.log`

### Erste Anmeldung

1. `https://<server-ip>:8443` öffnen.
2. Der Browser warnt wegen des selbstsignierten Zertifikats. Den angezeigten Fingerprint mit dem Fingerprint aus der Installer-Ausgabe vergleichen und nur bei Übereinstimmung fortfahren.
3. Mit `admin` und dem angezeigten Passwort anmelden, unter **Konto** ein neues Passwort setzen und `/root/hostmanaged-initial-admin.txt` löschen.

Eigenes Zertifikat (z. B. Let's Encrypt): Zertifikat und Schlüssel nach `/etc/hostmanaged/tls/server.crt` und `server.key` kopieren (Schlüssel `0640 root:hostmanaged`) und `systemctl restart hostmanaged` ausführen.

### Bridge für VMs im Hostnetz

VMs hängen standardmäßig am NAT-Netzwerk `default`. Damit VMs eigene öffentliche Adressen erhalten, wird eine Linux-Bridge benötigt (z. B. `vmbr0`). Der Installer richtet sie bewusst **nicht** automatisch ein, weil eine fehlerhafte Änderung die Fernwartung trennt. Vorgehen mit Konsolenzugang (KVM/IPMI/Rettungssystem des Anbieters) in [betrieb.md](betrieb.md#bridge-einrichten). Bei vielen Hostern müssen zusätzliche IPs außerdem an eine virtuelle MAC gebunden oder geroutet werden.

## Update

```bash
cd Host-managed
git pull
./installer/install.sh
```

Beim Update sichert der Installer zuerst die Datenbank und die Konfiguration (`/var/lib/hostmanaged/backups/`), stoppt Control Plane und Agent, tauscht die Programme und startet beide Dienste neu. Konfiguration, Zertifikat und Daten bleiben erhalten. Datenbankmigrationen laufen beim Start automatisch; ein älteres Programm verweigert den Start mit einer neueren Datenbank.

**Laufende VMs und Container werden durch ein Update nicht beendet**: Sie gehören zu libvirt bzw. LXC. Während des Neustarts (wenige Sekunden) sind Weboberfläche und API nicht erreichbar, offene Konsolen werden getrennt, laufende Jobs werden als „unterbrochen“ markiert.

Rollback: alte Version auschecken, `./installer/install.sh` erneut ausführen. Wurde das Datenbankschema geändert, zusätzlich die Sicherung aus `/var/lib/hostmanaged/backups/` zurückspielen (siehe unten).

## Deinstallation

```bash
./installer/uninstall.sh                 # Dienste und Programme entfernen
./installer/uninstall.sh --purge         # zusätzlich Konfiguration und Datenbank (vorher Abschlusssicherung nach /root)
./installer/uninstall.sh --purge-images  # zusätzlich alle VM-Datenträger und ISOs (mit Bestätigung)
```

VMs und Container laufen nach der Deinstallation weiter und bleiben mit `virsh` bzw. `lxc-*` verwaltbar.

## Sicherung der Verwaltungsdaten

```bash
hostmanaged backup            # → /var/lib/hostmanaged/backups/hostmanaged-<zeit>.tar.gz
hostmanaged backup -output /root/hm.tar.gz
```

Das Archiv enthält einen konsistenten Datenbank-Snapshot (Benutzer, Rollen, Tokens, Jobs, Audit-Protokoll) und `/etc/hostmanaged` einschließlich TLS-Schlüssel. Es wird mit Rechten `0600` angelegt und muss wie ein Geheimnis behandelt werden. VM-Datenträger sind **nicht** enthalten; sie werden über VM-Backups gesichert (Proxmox-Backup-Server-Anbindung geplant, siehe [status.md](status.md)).

## Wiederherstellung der Control Plane

1. Debian neu installieren und `./installer/install.sh` ausführen.
2. `systemctl stop hostmanaged hostmanaged-agent`
3. Archiv entpacken und zurückkopieren:
   ```bash
   mkdir /tmp/r && tar -C /tmp/r -xzf hostmanaged-<zeit>.tar.gz
   install -m 0640 -o hostmanaged -g hostmanaged /tmp/r/data/hostmanaged.db /var/lib/hostmanaged/hostmanaged.db
   rm -f /var/lib/hostmanaged/hostmanaged.db-wal /var/lib/hostmanaged/hostmanaged.db-shm
   cp -a /tmp/r/config/. /etc/hostmanaged/
   chown -R root:hostmanaged /etc/hostmanaged && chmod 0640 /etc/hostmanaged/tls/server.key
   ```
4. `systemctl start hostmanaged-agent hostmanaged`
5. VMs und Container werden von libvirt/LXC gelesen; liegen ihre Datenträger noch auf dem Host, erscheinen sie sofort. Nach einer Neuinstallation müssen die libvirt-Definitionen (`/etc/libvirt/qemu/*.xml`) und `/var/lib/lxc` aus einer Dateisicherung wiederhergestellt werden.

Administratorpasswort vergessen: `hostmanaged admin reset-password --username admin` (als root, beendet alle Sitzungen des Benutzers).

## Fehlersuche

| Symptom | Prüfen |
|---|---|
| Weboberfläche nicht erreichbar | `systemctl status hostmanaged`, `journalctl -u hostmanaged -n 50`, Firewall des Hosts/Anbieters für den Port |
| „Node-Agent nicht erreichbar“ | `systemctl status hostmanaged-agent`, `ls -l /run/hostmanaged/agent.sock` (Gruppe `hostmanaged`) |
| VM startet nicht: „Permission denied“ auf Datenträger | Datenträger muss für `libvirt-qemu` erreichbar sein; eigene Pools nicht unter `/root` oder Verzeichnissen mit `0700` anlegen |
| „/dev/kvm is not available“ | VT-x/AMD-V im BIOS aktivieren; `lsmod \| grep kvm`; bei vServern verschachtelte Virtualisierung beim Anbieter anfragen |
| NAT-Netzwerk `default` startet nicht | Adressbereich 192.168.122.0/24 wird bereits verwendet: `virsh net-edit default` und anderen Bereich wählen |
| Container ohne IP | Bridge vorhanden? `virsh net-list`; im Container `ip a`; DHCP-Client der Distribution |
| Container-Erstellung schlägt fehl | Internetzugang zu `images.linuxcontainers.org`; `/etc/subuid` enthält `root:100000:65536` |
| Zu viele fehlgeschlagene Anmeldungen | 15 Minuten warten oder Dienst neu starten |
