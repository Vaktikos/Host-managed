# Sicherheitskonzept

## Grundsätze

- Die Weboberfläche und die API laufen ohne Root-Rechte. Nur der Node-Agent hat Root-Rechte, und er führt nur fest definierte, typisierte Operationen aus.
- Kein Endpunkt nimmt Shell-Befehle oder frei formulierte Programmaufrufe entgegen. LXC-Programme werden mit fester Argumentliste ohne Shell gestartet; alle Werte werden vorher gegen Positivlisten geprüft (reguläre Ausdrücke, `netip`, Aufzählungen).
- Berechtigungen werden ausschließlich serverseitig geprüft. Die Oberfläche blendet Bedienelemente nur aus Komfortgründen aus.
- Jede Änderung und jeder verweigerte Zugriff wird protokolliert.

## Authentifizierung

| Mechanismus | Umsetzung |
|---|---|
| Passwörter | Argon2id (m=64 MiB, t=3, p=2), mindestens 12 Zeichen, zeitkonstanter Vergleich; unbekannte Benutzer durchlaufen denselben Hash-Aufwand |
| Sitzungen | 256-Bit-Zufallstoken im Cookie `hm_session` (`HttpOnly`, `Secure`, `SameSite=Strict`); in der Datenbank nur als SHA-256 gespeichert; Leerlaufzeit 2 h, Höchstdauer 12 h |
| CSRF | eigenes 256-Bit-Token pro Sitzung, Pflicht im Header `X-CSRF-Token` für alle nicht lesenden Anfragen |
| API-Tokens | `hm_<id>_<geheimnis>`, Geheimnis 256 Bit, nur SHA-256 gespeichert, Ablaufdatum, Widerruf, gleiche Rechte wie der Benutzer |
| Brute-Force-Schutz | 8 Fehlversuche je Benutzer, 20 je IP in 15 Minuten |
| Passwortänderung | erfordert das aktuelle Passwort und beendet alle anderen Sitzungen; Zurücksetzen durch Admin beendet alle Sitzungen |
| Konsolen | WebSocket mit Sitzungsprüfung; fremde Herkunft (Origin) wird abgelehnt |

## Transport

- HTTPS mit TLS 1.2 oder neuer. Der Installer erzeugt ein selbstsigniertes ECDSA-Zertifikat und zeigt den SHA-256-Fingerprint an, damit die erste Verbindung überprüft werden kann. `hostctl` kann den Fingerprint fest verankern (`HM_FINGERPRINT`).
- HTTP-Header: `Strict-Transport-Security`, `Content-Security-Policy` (nur eigene Skripte, keine Einbettung in Frames), `X-Content-Type-Options`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy`.
- `insecure_http = true` ist nur für die Entwicklung vorgesehen.

## Rollen und Berechtigungen

Siehe [betrieb.md](betrieb.md#benutzer-und-rollen). Berechtigungen sind feingranular (`vm.power`, `vm.console`, `vm.delete`, `storage.upload` …). Eine Rolle gilt derzeit für den ganzen Node; die Beschränkung auf einzelne Ressourcen ist geplant. Wer `vm.console` oder `ct.console` besitzt, erhält Zugriff auf den Gast bzw. Root im Container.

## Vertrauensgrenzen

Siehe [architektur.md](architektur.md#vertrauensgrenzen).

## Härtung

- `hostmanaged.service`: eigener Benutzer, `NoNewPrivileges`, `ProtectSystem=strict` (Schreibzugriff nur auf `/var/lib/hostmanaged`), keine Capabilities, Systemaufruf-Filter, `MemoryDenyWriteExecute`, eingeschränkte Adressfamilien.
- Agent-Socket: `0660 root:hostmanaged` in einem Verzeichnis `0750`; zusätzlich Prüfung der Peer-UID.
- Datenbank und Sicherungen nur für `hostmanaged` bzw. root lesbar; Sicherungsarchive `0600`.
- VNC ausschließlich über UNIX-Sockets, kein offener TCP-Port.
- Container unprivilegiert mit User-Namespace und AppArmor.
- Uploads werden unter temporärem Namen geschrieben, Dateinamen geprüft (keine Pfadbestandteile), Zielpfad muss im Pool liegen.
- JSON-Anfragen mit Größenlimit (1 MiB) und Ablehnung unbekannter Felder.

## Audit

Das Audit-Protokoll speichert Benutzer, Zeitpunkt, Quell-IP, Aktion, Ressource, Ergebnis, Korrelations-ID (= Request-ID) und Details. Passwörter und Tokens werden nie protokolliert. Datenbank-Trigger verhindern Änderungen und Löschungen durch die Anwendung; eine SHA-256-Hash-Kette macht Änderungen direkt in der Datenbankdatei erkennbar (*Audit-Protokoll → Integrität prüfen*). Gegen einen Angreifer mit Root-Rechten, der die gesamte Kette neu berechnet, hilft nur eine externe Kopie (Weiterleitung an ein externes Log-System ist geplant).

## Geheimnisse

- Keine Geheimnisse im Repository. Die Konfigurationsdatei enthält keine Geheimnisse.
- TLS-Schlüssel: `/etc/hostmanaged/tls/server.key`, `0640 root:hostmanaged`.
- Initiales Administratorpasswort: nur im Terminal und in `/root/hostmanaged-initial-admin.txt` (`0600`), nicht im Installationsprotokoll.
- Zugangsdaten für externe Systeme (Proxmox Backup Server, LDAP) werden verschlüsselt gespeichert, sobald diese Anbindungen umgesetzt sind.

## Offene Punkte

- MFA (TOTP) und Ressourcen-ACLs fehlen noch.
- Kein Schutz gegen einen kompromittierten Root-Benutzer auf dem Host (by design).
- Externe Sicherheitsprüfung steht aus.

Sicherheitslücken bitte nicht öffentlich melden, sondern direkt an die Projektverantwortlichen.
