# Security concept

## Principles

- The web UI and API run without root privileges. Only the node agent runs as root, and it only performs fixed, typed operations.
- No endpoint accepts shell commands or free-form program invocations. LXC tools are started with fixed argv and no shell; every value is checked against allow-lists first (regular expressions, `netip`, enumerations).
- Permissions are only enforced server-side. The UI hides controls for convenience only.
- Every change and every denied request is logged.

## Authentication

| Mechanism | Implementation |
|---|---|
| Passwords | Argon2id (m=64 MiB, t=3, p=2), at least 12 characters, constant-time comparison; unknown users pay the same hashing cost |
| Sessions | 256-bit random token in cookie `hm_session` (`HttpOnly`, `Secure`, `SameSite=Strict`); stored only as SHA-256; idle timeout 2 h, maximum 12 h |
| CSRF | separate 256-bit token per session, required in header `X-CSRF-Token` on every non-read request |
| API tokens | `hm_<id>_<secret>`, 256-bit secret, only SHA-256 stored, expiry, revocation, same rights as the user |
| Brute force | 8 failures per user, 20 per IP within 15 minutes |
| Password change | requires the current password and signs out all other sessions; an admin reset signs out all sessions |
| Consoles | WebSocket with session check; foreign origins are rejected |

## Transport

- HTTPS with TLS 1.2 or newer. The installer creates a self-signed ECDSA certificate and prints its SHA-256 fingerprint so the first connection can be verified. `hostctl` can pin the fingerprint (`HM_FINGERPRINT`).
- HTTP headers: `Strict-Transport-Security`, `Content-Security-Policy` (own scripts only, no framing), `X-Content-Type-Options`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy`.
- `insecure_http = true` is for development only.

## Roles and permissions

See [operations.md](operations.md#users-and-roles). Permissions are fine-grained (`vm.power`, `vm.console`, `vm.delete`, `storage.upload` …). A role currently applies to the whole node; per-resource restriction is planned. Holding `vm.console` or `ct.console` grants access to the guest or root inside the container.

## Trust boundaries

See [architecture.md](architecture.md#trust-boundaries).

## Hardening

- `hostmanaged.service`: own user, `NoNewPrivileges`, `ProtectSystem=strict` (write access only to `/var/lib/hostmanaged`), no capabilities, syscall filter, `MemoryDenyWriteExecute`, restricted address families.
- Agent socket: `0660 root:hostmanaged` inside a `0750` directory; peer UID checked as well.
- Database and backups readable only by `hostmanaged` or root; backup archives `0600`.
- VNC only via UNIX sockets, no open TCP port.
- Containers unprivileged with user namespace and AppArmor.
- Uploads are written under a temporary name, file names are checked (no path components), the target must be inside the pool.
- JSON requests with a size limit (1 MiB); unknown fields are rejected.

## Audit

The audit log stores user, time, source IP, action, resource, result, correlation ID (= request ID) and details. Passwords and tokens are never logged. Database triggers prevent updates and deletes through the application; a SHA-256 hash chain makes edits made directly in the database file detectable (*Audit log → Verify integrity*). Against an attacker with root who recomputes the whole chain only an external copy helps (forwarding to an external log system is planned).

## Secrets

- No secrets in the repository. The configuration file contains no secrets.
- TLS key: `/etc/hostmanaged/tls/server.key`, `0640 root:hostmanaged`.
- Initial admin password: terminal and `/root/hostmanaged-initial-admin.txt` (`0600`) only, never in the install log.
- Credentials for external systems (Proxmox Backup Server, LDAP) will be stored encrypted once those integrations exist.

## Open items

- MFA (TOTP) and resource ACLs are missing.
- No protection against a compromised root user on the host (by design).
- An external security review is pending.

Please report vulnerabilities privately to the maintainers, not publicly.
