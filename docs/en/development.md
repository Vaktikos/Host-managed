# Development and release process

## Requirements

- Go 1.26 (the module requires it; `GOTOOLCHAIN=auto` downloads it)
- Node.js 18+ and npm
- For integration tests: root, `libvirt-daemon-system`, `qemu-system-x86`, `qemu-utils`, `ovmf`, `lxc`, `uidmap`

## Build and test

```bash
make build            # web UI (embedded) + bin/hostmanaged + bin/hostctl
make test             # go vet, gofmt, go test -race, TypeScript check, translation tests
sudo make test-integration   # real libvirt/QEMU and LXC; creates and deletes VMs/containers
```

## Running locally

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
cd web && npm run dev          # Vite on :5173, proxies /api to :18443
```

## Conventions

- Every user-visible text goes through i18next with keys in both `web/src/i18n/de.json` and `en.json`. `npm test` fails if a key is missing in one language or used in code without a translation.
- Backend errors use the uniform format `{"error":{"code","message","field","request_id"}}`; the UI maps `code` to translations (`errors.<code>`).
- Anything that runs as root lives in `internal/agent` and its adapters. New agent operations need a typed request in `internal/agentapi` with validation that runs in both the control plane and the agent.
- No shell invocations. External programs are called with fixed argv.
- Long operations run as jobs; never report progress that is not measured.
- Test doubles (like the fake agent in `internal/api/api_test.go`) are for tests only.
- Database changes: append a new entry to `migrations` in `internal/store/store.go`; never edit released migrations.

## Release process

1. Update `docs/*/status.md` and the release notes (`CHANGELOG.md`).
2. Tag: `git tag -s vX.Y.Z && git push --tags`.
3. CI builds with `make release` and produces `dist/host-managed-<version>-linux-amd64.tar.gz` and `SHA256SUMS`. Builds are reproducible as far as Go (`-trimpath`) and npm lockfiles allow.
4. Sign `SHA256SUMS` (e.g. `gpg --detach-sign` or minisign) and attach everything to the release. Signed APT packages are planned.
5. Test the upgrade path on a VM with the previous release before publishing.
