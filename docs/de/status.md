# Umsetzungsstand, Tests und bekannte Einschränkungen

Stand: Meilenstein 1 (Single Node). „Umgesetzt“ heißt: Oberfläche (falls nötig), API, Backend-Logik, Berechtigungsprüfung, Fehlerbehandlung, Tests und Dokumentation sind vorhanden. Alles andere ist ausdrücklich als *geplant* oder *nicht begonnen* markiert und wird in der Oberfläche nicht als verfügbar angeboten.

## Funktionsübersicht

| Bereich | Funktion | Stand |
|---|---|---|
| Installation | Installer für Debian 12/13 (Prüfungen, Pakete, Pools, TLS, Dienste, erster Admin, Update mit Sicherung) | umgesetzt, auf Debian 13 getestet |
| | Deinstallation, Wiederherstellung der Control Plane | umgesetzt (Deinstallation getestet: nein) |
| | Bootfähiges ISO | nicht begonnen |
| Anmeldung | lokale Benutzer, Argon2id, Sitzungen, CSRF, Brute-Force-Schutz, Passwortänderung, Sitzungsübersicht | umgesetzt |
| | API-Tokens mit Ablauf und Widerruf | umgesetzt |
| | TOTP/MFA mit QR-Code, Wiederherstellungscodes, Reset durch Admin/CLI; Schlüssel AES-GCM-verschlüsselt | umgesetzt |
| | LDAP/AD, OIDC | geplant |
| Rechte | 7 Rollen, serverseitige Prüfung, Schutz des letzten Admins | umgesetzt |
| | Rechte auf einzelne Ressourcen (ACL-Pfade), eigene Rollen | geplant |
| Audit | Protokoll aller Änderungen, Anmeldungen, verweigerter Zugriffe; nur anhängbar; Hash-Kette mit Prüfung | umgesetzt |
| VMs | erstellen (UEFI/BIOS, Secure Boot, TPM, VirtIO/SATA/SCSI, ISO, NAT/Bridge, Guest-Agent-Kanal) | umgesetzt |
| | starten, herunterfahren, ausschalten, neu starten, zurücksetzen, pausieren/fortsetzen | umgesetzt |
| | löschen mit/ohne Datenträger, Sperre gegen parallele Operationen | umgesetzt |
| | CPU/RAM ändern, Beschreibung, Tags, Notizen, Autostart, Template-Markierung | umgesetzt |
| | VNC-Konsole im Browser | umgesetzt |
| | Live-Statistiken (CPU, RAM-Balloon, Netz, Disk), Gast-IPs über Guest Agent | umgesetzt |
| | Datenträger hinzufügen/entfernen/vergrößern, NICs hinzufügen/entfernen, ISO wechseln, Boot-Reihenfolge; Hotplug wo QEMU es kann | umgesetzt |
| | Klonen (voll; verknüpft von Templates), Cloud-Init (NoCloud), VMs aus Cloud-Images | umgesetzt |
| | Snapshots (intern, mit RAM bei laufenden BIOS-VMs; laufende UEFI-VMs nicht möglich – QEMU-Grenze), Revert, Löschen | umgesetzt |
| | serielle Konsole, SPICE | geplant |
| | PCI-/GPU-Passthrough, SR-IOV, NUMA | geplant |
| | Startreihenfolge, Startverzögerung, Abhängigkeiten | geplant (nur Autostart) |
| Container | erstellen aus offiziellen LXC-Abbildern, unprivilegiert, Limits, statische IPv4/DHCP | umgesetzt |
| | starten, herunterfahren, stoppen, neu starten, einfrieren, löschen | umgesetzt |
| | Konsole im Browser | umgesetzt |
| | IPv6-Konfiguration, Bind-Mounts, Festplattenkontingente, Klonen | geplant |
| Storage | Pools und Volumes anzeigen, ISO hochladen, Download per URL (SSRF-Schutz, Prüfsumme), Volumes löschen (mit Nutzungsprüfung) | umgesetzt |
| | Pools anlegen/entfernen (LVM-Thin, ZFS, NFS, iSCSI, Ceph RBD) über die Oberfläche | geplant |
| | Datenträger vergrößern/verschieben, SMART | geplant |
| Netzwerk | Schnittstellen, Bridges, Bonds, VLANs und libvirt-Netze anzeigen | umgesetzt |
| | Änderungen mit Vorschau und automatischem Rollback | geplant |
| | Firewall (nftables) | geplant |
| Jobs | persistente Jobs, Protokoll, gemessener Fortschritt, Abbruch, Kennzeichnung unterbrochener Jobs | umgesetzt |
| Backups | Sicherung der Verwaltungsdaten (`hostmanaged backup`) | umgesetzt |
| | lokale VM-/CT-Backups auf Backup-Speicher (Verzeichnis-/NFS-Pools `backup`, `*-backup`): laufende VMs zeitpunktgenau per libvirt-Backup-Job (mit Gast-Agent anwendungskonsistent), Offline-Kopie; Container Stoppen/Einfrieren/Live | umgesetzt |
| | Wiederherstellung (neuer Name oder Desaster-Recovery mit ursprünglicher UUID/MAC), Schutz, Aufbewahrung (PBS-Regeln), Zeitpläne mit Wochentagen/Uhrzeit, sofort ausführen | umgesetzt |
| | inkrementelle/deduplizierte Backups, Verschlüsselung, Einzeldatei-Wiederherstellung, Prüfjobs | geplant |
| | Proxmox Backup Server (Verbindung, VM-/CT-Backups, Zeitpläne, Restore, Import von Proxmox-Backups) | geplant, Konzept in [architektur.md](architektur.md) |
| Cluster | Node-Agent über mTLS, Cluster, Migration, HA mit Fencing | geplant, Konzept in [architektur.md](architektur.md) |
| Mandanten | Mandanten, Projekte, Kontingente, Reseller-API, Webhooks | geplant |
| Monitoring | Benachrichtigungen (E-Mail, Webhook), Schwellenwerte, Metrik-Langzeitspeicher | geplant |
| Oberfläche | Deutsch/Englisch, Ressourcenbaum, globale Suche, Tabellen mit Sortierung/Filter/Spaltenauswahl, Aufgabenleiste, responsiv | umgesetzt |
| CLI | `hostctl` für Node, VMs, Container, Storage, Backups, Jobs | umgesetzt |
| API | REST v1, OpenAPI-Beschreibung, einheitliche Fehler, Job-IDs | umgesetzt |
| | SSE/WebSocket für Statusänderungen, Webhooks, Idempotenzschlüssel | geplant (Statusabfrage per Polling) |

## Tests

| Test | Umgebung | Ergebnis |
|---|---|---|
| Go-Unit-Tests (`make test-go`, mit `-race`) | Store, Auth, Jobs, API (mit Test-Double für den Agent), LXC (Argumentprüfung), Host-Infos, Konfiguration | bestanden |
| Frontend (`make test-web`) | TypeScript-Prüfung, Übersetzungen: gleiche Schlüssel in DE/EN, alle verwendeten Schlüssel vorhanden, Platzhalter identisch | bestanden |
| libvirt-Integration (`HM_LIBVIRT_INTEGRATION=1`) | echter libvirtd 10.0 + QEMU 8.2, Software-Emulation (kein KVM in der Testumgebung): erstellen, starten, VNC-Socket (RFB-Begrüßung), Statistiken, Pause, Ressourcen, Metadaten, Template, Datenträger-Schutz, Löschen | bestanden |
| LXC-Integration (`HM_LXC_INTEGRATION=1`) | echtes LXC 5.0: Alpine-Container erstellen, starten, User-Namespace prüfen, stoppen, löschen | bestanden |
| End-to-End im Browser (Playwright) | Anmeldung, Sprachwechsel, VM über Dialog anlegen, Container anlegen, Container-Konsole bedienen, VNC-Konsole | bestanden |
| Cloud-Init (`HM_CLOUD_IMAGE`) | Debian-13-Cloud-Image unter echtem libvirt: Import, Vergrößern, Seed, Boot, DHCP, SSH-Login mit Schlüssel, Hostname, sudo; Klon mit eigenem Seed | bestanden |
| Hardware/Snapshots/Klone | echter libvirt: Hotplug Disk/NIC, SATA-Fallback, Online-Vergrößern, ISO, Boot-Reihenfolge, RAM-Snapshot + Revert, Voll-/verknüpfter Klon, Schutz der Template-Basis | bestanden |
| MFA im Browser | Einrichtung mit QR, falscher Code, Login mit TOTP, Deaktivieren per Wiederherstellungscode | bestanden |
| Backups (libvirt-/LXC-Integration) | Push-Backup laufender VM, Offline-Backup, Wiederherstellung mit neuer Identität und mit erhaltener UUID/MAC, Ablehnung bei Namenskonflikt; Container im Modus Stoppen/Einfrieren/Live, Wiederherstellung unter neuem Namen mit Eigentümern (verschobene UIDs) und angepasster Konfiguration | bestanden |
| Backups End-to-End | CLI und Browser: Sicherung, Wiederherstellung + Start, Schutz, Aufräum-Vorschau, Zeitplan anlegen und sofort ausführen (mit Aufräumen) | bestanden |
| Installer | Debian 13 Cloud-Image (systemd) in QEMU: Erstinstallation, Update mit Sicherung, Neustart der Dienste, VM mit UEFI + Secure Boot + TPM per verschachteltem KVM, Debian-Container mit DHCP; Bau aus dem Quellcode mit geprüftem Go-Archiv | bestanden |
| Installer auf Debian 12 | – | **nicht getestet** |
| Installer auf echter Hardware | – | **nicht getestet** |
| Lasttests, große Inventare | – | **nicht durchgeführt** |

Fehlerfälle mit Tests: ungültiges/widerrufenes/manipuliertes API-Token, fehlende Berechtigung, fehlendes/falsches CSRF-Token, Brute-Force-Sperre, voller Storage (Job schlägt mit echter Meldung fehl), Agent nicht erreichbar (503), doppelter VM-Name, Löschen laufender VM/Container, Löschen benutzter Volumes, Manipulation des Audit-Protokolls, neuere Datenbank als Programm, abgebrochene und abstürzende Jobs, Begrenzung paralleler Jobs.

## Bekannte Einschränkungen

- Single Node: Control Plane und Agent laufen auf demselben Host.
- Echtzeitstatus per Polling (3–5 s), nicht per Push.
- Job-Protokolle sind technische Meldungen auf Englisch.
- Validierungsmeldungen der API sind englisch; die Oberfläche prüft Eingaben vorher selbst und zeigt übersetzte Meldungen.
- Backups sind Vollkopien (qcow2 pro Datenträger, tar.gz pro Container); noch keine Deduplizierung oder inkrementellen Backups. Ein Backup-Speicher auf derselben Platte wie die VMs schützt nicht vor Plattenausfall.
- Laufende UEFI-VMs: der NVRAM wird im laufenden Betrieb kopiert und kann minimal älter sein als der Plattenstand.
- Ein unter neuem Namen wiederhergestellter Container behält im Gast den alten Hostnamen (`/etc/hostname` wird aus Sicherheitsgründen nicht vom Host aus geändert).
- Zeitpläne laufen in der Zeitzone des Servers; ein um mehr als eine Stunde verpasster Termin wird übersprungen (Audit-Eintrag `backup.schedule.missed`).
- Statische Container-IP wird über die LXC-Konfiguration gesetzt; eine Distribution mit aktivem DHCP-Client bezieht zusätzlich eine DHCP-Adresse.
- Der Installer richtet keine Bridge ein (siehe [betrieb.md](betrieb.md#bridge-einrichten)).
- Ohne Hardware-Virtualisierung (viele vServer) sind VMs nur per Software-Emulation möglich.
