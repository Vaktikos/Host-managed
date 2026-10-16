# Entwicklung und Release-Prozess

## Voraussetzungen

- Go 1.26 (vom Modul verlangt; `GOTOOLCHAIN=auto` lädt es automatisch)
- Node.js 18+ und npm
- Für Integrationstests: root, `libvirt-daemon-system`, `qemu-system-x86`, `qemu-utils`, `ovmf`, `lxc`, `uidmap`

## Bauen und testen

```bash
make build            # Weboberfläche (eingebettet) + bin/hostmanaged + bin/hostctl
make test             # go vet, gofmt, go test -race, TypeScript-Prüfung, Übersetzungstests
sudo make test-integration   # echtes libvirt/QEMU und LXC; legt VMs/Container an und löscht sie
```

## Lokal starten

```bash
cat > dev.conf <<EOF
listen = 127.0.0.1:18443
data_dir = ./tmp/data
agent_socket = ./tmp/agent.sock
agent_group = root
allowed_user = root
allow_software_emulation = true
insecure_http = true
EOF
sudo ./bin/hostmanaged agent -config dev.conf &
./bin/hostmanaged admin create -config dev.conf --username admin
./bin/hostmanaged server -config dev.conf &
cd web && npm run dev          # Vite auf :5173, leitet /api an :18443 weiter
```

## Konventionen

- Jeder sichtbare Text läuft über i18next, mit Schlüsseln in `web/src/i18n/de.json` und `en.json`. `npm test` schlägt fehl, wenn ein Schlüssel in einer Sprache fehlt oder im Code ohne Übersetzung verwendet wird.
- Fehler des Backends haben das Format `{"error":{"code","message","field","request_id"}}`; die Oberfläche übersetzt `code` (`errors.<code>`).
- Alles, was Root-Rechte braucht, liegt in `internal/agent` und den Adaptern. Neue Agent-Operationen benötigen einen typisierten Request in `internal/agentapi` mit Validierung, die in Control Plane und Agent läuft.
- Keine Shell-Aufrufe. Externe Programme mit fester Argumentliste.
- Lange Operationen laufen als Job; kein Fortschritt melden, der nicht gemessen wird.
- Test-Doubles (z. B. der Fake-Agent in `internal/api/api_test.go`) nur in Tests.
- Datenbankänderungen: neuen Eintrag an `migrations` in `internal/store/store.go` anhängen; veröffentlichte Migrationen nie ändern.

## Release-Prozess

1. `docs/*/status.md` und Release Notes (`CHANGELOG.md`) aktualisieren.
2. Tag setzen: `git tag -s vX.Y.Z && git push --tags`.
3. Die CI baut mit `make release` `dist/host-managed-<version>-linux-amd64.tar.gz` und `SHA256SUMS`. Builds sind reproduzierbar, soweit Go (`-trimpath`) und npm-Lockfiles es zulassen.
4. `SHA256SUMS` signieren (z. B. `gpg --detach-sign` oder minisign) und alles an das Release anhängen. Signierte APT-Pakete sind geplant.
5. Vor der Veröffentlichung den Update-Pfad auf einer VM mit dem vorherigen Release testen.
