#!/usr/bin/env bash
# Host-managed installer for Debian 12/13 (Ubuntu 22.04/24.04 best effort).
#
# Safe by design:
#   * never changes network interfaces, bridges, routes or firewall rules
#   * never partitions or formats disks
#   * idempotent: re-running upgrades binaries and keeps configuration/data
#
# Usage:  sudo ./installer/install.sh [options]
#   --binary-dir DIR          use prebuilt binaries from DIR (hostmanaged, hostctl)
#   --port PORT               HTTPS port of the web interface (default 8443)
#   --admin USER              name of the first administrator (default admin)
#   --allow-no-kvm            continue without /dev/kvm (VMs use slow software emulation)
#   --skip-packages           do not install Debian packages
#   --yes                     do not ask for confirmation
#   --lang de|en              installer language (default: from $LANG, else de)
set -Eeuo pipefail

LOG=/var/log/hostmanaged-install.log
PORT=8443
ADMIN=admin
BIN_DIR=""
ALLOW_NO_KVM=0
SKIP_PKGS=0
ASSUME_YES=0
LANGSEL=""
STORAGE_DIR=/var/lib/hostmanaged-storage
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

while [ $# -gt 0 ]; do
  case "$1" in
    --binary-dir) BIN_DIR="$2"; shift 2 ;;
    --port) PORT="$2"; shift 2 ;;
    --admin) ADMIN="$2"; shift 2 ;;
    --allow-no-kvm) ALLOW_NO_KVM=1; shift ;;
    --skip-packages) SKIP_PKGS=1; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --lang) LANGSEL="$2"; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

if [ -z "$LANGSEL" ]; then
  case "${LANG:-}" in en*) LANGSEL=en ;; *) LANGSEL=de ;; esac
fi
# m "german" "english"
m() { if [ "$LANGSEL" = de ]; then printf '%s' "$1"; else printf '%s' "$2"; fi; }

STEP=0
step() { STEP=$((STEP + 1)); printf '\n\033[1m[%d] %s\033[0m\n' "$STEP" "$1"; }
ok()   { printf '    \033[32mOK\033[0m   %s\n' "$1"; }
warn() { printf '    \033[33mWARN\033[0m %s\n' "$1"; }
die()  { printf '\n\033[31m%s\033[0m %s\n' "$(m 'ABBRUCH:' 'ABORTED:')" "$1" >&2; printf '%s %s\n' "$(m 'Protokoll:' 'Log:')" "$LOG" >&2; exit 1; }
trap 'die "$(m "Fehler in Zeile" "error at line") $LINENO: $BASH_COMMAND"' ERR

[ "$(id -u)" -eq 0 ] || { echo "$(m 'Bitte als root ausführen (sudo).' 'Please run as root (sudo).')" >&2; exit 1; }
[[ "$PORT" =~ ^[0-9]+$ ]] && [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "invalid --port"
[[ "$ADMIN" =~ ^[a-z][a-z0-9._-]{1,31}$ ]] || die "invalid --admin"

# Log everything except secrets (the admin password goes to the terminal only).
exec 3>&1
exec > >(tee -a "$LOG") 2>&1
echo "=== $(date -Is) Host-managed installer ==="

########################################################################
step "$(m 'Systemprüfung' 'System checks')"
. /etc/os-release
case "${ID}:${VERSION_ID:-}" in
  debian:12|debian:13) ok "$PRETTY_NAME" ;;
  ubuntu:22.04|ubuntu:24.04) warn "$PRETTY_NAME – $(m 'nicht offiziell unterstützt, wird versucht' 'not officially supported, trying anyway')" ;;
  *) die "$(m 'Nicht unterstütztes System' 'Unsupported system'): $PRETTY_NAME ($(m 'unterstützt: Debian 12/13' 'supported: Debian 12/13'))" ;;
esac
command -v systemctl >/dev/null || die "systemd $(m 'wird benötigt' 'is required')"

ARCH="$(dpkg --print-architecture)"
case "$ARCH" in
  amd64) QEMU_PKG=qemu-system-x86; FW_PKG=ovmf ;;
  arm64) QEMU_PKG=qemu-system-arm; FW_PKG=qemu-efi-aarch64 ;;
  *) die "$(m 'Nicht unterstützte Architektur' 'Unsupported architecture'): $ARCH" ;;
esac
ok "$(m 'Architektur' 'Architecture'): $ARCH"

if grep -qE '\b(vmx|svm)\b' /proc/cpuinfo; then
  ok "$(m 'CPU-Virtualisierung vorhanden' 'CPU virtualization present') ($(grep -oE '\b(vmx|svm)\b' /proc/cpuinfo | head -1))"
  VIRT_CPU=1
else
  VIRT_CPU=0
  warn "$(m 'Keine vmx/svm-Flags – VT-x/AMD-V im BIOS aktivieren oder (bei einem vServer) verschachtelte Virtualisierung beim Anbieter freischalten lassen.' 'No vmx/svm flags – enable VT-x/AMD-V in firmware or (on a VPS) ask your provider for nested virtualization.')"
fi

if [ -d /sys/firmware/efi ]; then ok "$(m 'Boot-Modus' 'Boot mode'): UEFI"; else ok "$(m 'Boot-Modus' 'Boot mode'): BIOS"; fi

MEM_KB=$(awk '/MemTotal/ {print $2}' /proc/meminfo)
if [ "$MEM_KB" -lt 2000000 ]; then warn "$(m 'Wenig RAM' 'Low memory'): $((MEM_KB / 1024)) MiB"; else ok "RAM: $((MEM_KB / 1024)) MiB"; fi
mkdir -p /var/lib
FREE_KB=$(df -Pk /var/lib | awk 'NR==2 {print $4}')
if [ "$FREE_KB" -lt 10485760 ]; then warn "$(m 'Weniger als 10 GiB frei unter /var/lib' 'Less than 10 GiB free under /var/lib'): $((FREE_KB / 1048576)) GiB"; else ok "$(m 'Freier Speicher /var/lib' 'Free space /var/lib'): $((FREE_KB / 1048576)) GiB"; fi

MGMT_IP="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -1 || true)"
MGMT_IP6="$(ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -1 || true)"
ok "$(m 'Management-Adresse' 'Management address'): ${MGMT_IP:-?} ${MGMT_IP6:+/ $MGMT_IP6} ($(m 'wird nicht verändert' 'left unchanged'))"

LISTENERS="$(ss -ltnH "( sport = :$PORT )" 2>/dev/null || true)"
if [ -n "$LISTENERS" ] && ! systemctl is-active --quiet hostmanaged 2>/dev/null; then
  die "$(m 'Port' 'Port') $PORT $(m 'ist bereits belegt; anderen Port mit --port wählen' 'is already in use; choose another with --port')"
fi

########################################################################
step "$(m 'Programmdateien' 'Program files')"
if [ -z "$BIN_DIR" ]; then
  if [ -x "$SRC_DIR/bin/hostmanaged" ] && [ -x "$SRC_DIR/bin/hostctl" ] && [ -z "${HM_REBUILD:-}" ]; then
    BIN_DIR="$SRC_DIR/bin"
  elif [ -f "$SRC_DIR/go.mod" ]; then
    echo "    $(m 'Keine fertigen Binärdateien gefunden – baue aus dem Quellcode' 'No prebuilt binaries – building from source')"
    BUILD_FROM_SOURCE=1
  else
    die "$(m 'Weder --binary-dir noch Quellcode gefunden' 'Neither --binary-dir nor source tree found')"
  fi
fi

if [ "$ASSUME_YES" -ne 1 ] && [ -t 0 ]; then
  echo
  echo "$(m 'Es werden Pakete installiert (QEMU/KVM, libvirt, LXC), die Dienste hostmanaged und hostmanaged-agent eingerichtet und Port' 'This installs packages (QEMU/KVM, libvirt, LXC), sets up the hostmanaged and hostmanaged-agent services and opens HTTPS port') $PORT $(m 'für HTTPS verwendet. Netzwerk und Datenträger werden nicht verändert.' '. Network and disks are not modified.')"
  read -r -p "$(m 'Fortfahren? [j/N] ' 'Continue? [y/N] ')" ans
  case "$ans" in j|J|y|Y|ja|yes) ;; *) echo "$(m 'Abgebrochen.' 'Aborted.')"; exit 1 ;; esac
fi

########################################################################
step "$(m 'Pakete' 'Packages')"
PKGS=(
  "$QEMU_PKG" qemu-utils "$FW_PKG" swtpm swtpm-tools
  libvirt-daemon-system libvirt-clients libvirt-daemon-config-network dnsmasq-base
  lxc uidmap wget xz-utils gnupg dirmngr genisoimage
  openssl ca-certificates iproute2
)
if [ "$SKIP_PKGS" -eq 1 ]; then
  warn "$(m 'Paketinstallation übersprungen' 'Package installation skipped')"
else
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -q
  # Recommended packages are skipped to avoid pulling in network managers.
  apt-get install -y -q --no-install-recommends "${PKGS[@]}"
  ok "$(m 'Pakete installiert' 'Packages installed')"
fi

if [ "${BUILD_FROM_SOURCE:-0}" = 1 ]; then
  # Debian's Go is too old on Debian 12, so a pinned Go release is fetched
  # and verified against the checksum below (from dl.google.com/go/*.sha256).
  GO_VERSION=1.26.0
  case "$ARCH" in
    amd64) GO_SHA256=aac1b08a0fb0c4e0a7c1555beb7b59180b05dfc5a3d62e40e9de90cd42f88235 ;;
    arm64) GO_SHA256=bd03b743eb6eb4193ea3c3fd3956546bf0e3ca5b7076c8226334afe6b75704cd ;;
  esac
  apt-get install -y -q --no-install-recommends nodejs npm make git
  GOROOT_HM=/usr/local/lib/hostmanaged-go
  if [ "$("$GOROOT_HM/bin/go" env GOVERSION 2>/dev/null || true)" != "go$GO_VERSION" ]; then
    TMP_GO="$(mktemp)"
    wget -q -O "$TMP_GO" "https://dl.google.com/go/go$GO_VERSION.linux-$ARCH.tar.gz"
    echo "$GO_SHA256  $TMP_GO" | sha256sum -c --quiet - || die "$(m 'Prüfsumme des Go-Archivs stimmt nicht' 'Go archive checksum mismatch')"
    rm -rf "$GOROOT_HM" && mkdir -p "$GOROOT_HM"
    tar -C "$GOROOT_HM" --strip-components=1 -xzf "$TMP_GO"
    rm -f "$TMP_GO"
  fi
  ok "Go $GO_VERSION ($(m 'Prüfsumme bestätigt' 'checksum verified'))"
  ( cd "$SRC_DIR" && PATH="$GOROOT_HM/bin:$PATH" GOTOOLCHAIN=local make build )
  BIN_DIR="$SRC_DIR/bin"
fi
[ -x "$BIN_DIR/hostmanaged" ] || die "$BIN_DIR/hostmanaged $(m 'fehlt' 'missing')"
NEW_VERSION="$("$BIN_DIR/hostmanaged" version)"
ok "$(m 'Version' 'Version'): $NEW_VERSION"

########################################################################
step "$(m 'KVM' 'KVM')"
if [ "$VIRT_CPU" = 1 ]; then
  if grep -q vmx /proc/cpuinfo; then modprobe kvm_intel 2>/dev/null || true; else modprobe kvm_amd 2>/dev/null || true; fi
fi
if [ -e /dev/kvm ]; then
  ok "/dev/kvm $(m 'vorhanden' 'present')"
  SOFTEMU=false
elif [ "$ALLOW_NO_KVM" = 1 ]; then
  warn "$(m '/dev/kvm fehlt – VMs laufen mit Software-Emulation (sehr langsam)' '/dev/kvm missing – VMs use software emulation (very slow)')"
  SOFTEMU=true
else
  die "$(m '/dev/kvm fehlt. Hardware-Virtualisierung aktivieren oder mit --allow-no-kvm fortfahren (nur zum Testen).' '/dev/kvm is missing. Enable hardware virtualization or rerun with --allow-no-kvm (testing only).')"
fi

########################################################################
step "$(m 'Dienstkonto und Verzeichnisse' 'Service account and directories')"
getent group hostmanaged >/dev/null || groupadd --system hostmanaged
id hostmanaged >/dev/null 2>&1 || useradd --system --gid hostmanaged --home-dir /var/lib/hostmanaged --shell /usr/sbin/nologin hostmanaged
install -d -m 0750 -o root -g hostmanaged /etc/hostmanaged /etc/hostmanaged/tls
install -d -m 0750 -o hostmanaged -g hostmanaged /var/lib/hostmanaged /var/lib/hostmanaged/backups
# VM disks and ISO images live outside the control plane's private data
# directory: QEMU runs as libvirt-qemu and must be able to reach them.
install -d -m 0755 -o root -g root "$STORAGE_DIR" "$STORAGE_DIR/images" "$STORAGE_DIR/iso"
ok "$(m 'Benutzer hostmanaged, /etc/hostmanaged, /var/lib/hostmanaged' 'user hostmanaged, /etc/hostmanaged, /var/lib/hostmanaged'), $STORAGE_DIR"

# Subordinate IDs for unprivileged containers owned by root.
if ! grep -q '^root:' /etc/subuid 2>/dev/null; then echo "root:100000:65536" >> /etc/subuid; fi
if ! grep -q '^root:' /etc/subgid 2>/dev/null; then echo "root:100000:65536" >> /etc/subgid; fi
ok "$(m 'subuid/subgid für unprivilegierte Container' 'subuid/subgid for unprivileged containers')"

########################################################################
step "libvirt"
systemctl enable --now libvirtd >/dev/null 2>&1 || systemctl enable --now virtqemud >/dev/null 2>&1 || true
for i in $(seq 1 20); do [ -S /var/run/libvirt/libvirt-sock ] && break; sleep 0.5; done
[ -S /var/run/libvirt/libvirt-sock ] || die "$(m 'libvirtd läuft nicht' 'libvirtd is not running')"
ok "libvirtd $(m 'läuft' 'running')"

define_pool() {
  local name="$1" dir="$2" current
  if virsh -q pool-info "$name" >/dev/null 2>&1; then
    current="$(virsh -q pool-dumpxml "$name" | sed -n 's:.*<path>\(.*\)</path>.*:\1:p' | head -1 || true)"
    [ "$current" = "$dir" ] || warn "$(m 'Vorhandener Pool' 'Existing pool') $name → $current ($(m 'erwartet' 'expected') $dir; $(m 'unverändert gelassen' 'left unchanged'))"
  else
    virsh -q pool-define-as "$name" dir --target "$dir" >/dev/null
    ok "$(m 'Storage-Pool angelegt' 'Storage pool defined'): $name → $dir"
  fi
  virsh -q pool-autostart "$name" >/dev/null 2>&1 || true
  # Capture first: with pipefail, "cmd | grep -q" can fail through SIGPIPE.
  local info
  info="$(virsh -q pool-info "$name")"
  [[ "$info" =~ State:[[:space:]]+running ]] || virsh -q pool-start "$name" >/dev/null
}
define_pool local "$STORAGE_DIR/images"
define_pool iso "$STORAGE_DIR/iso"

# The NAT network "default" (virbr0, 192.168.122.0/24) is created by libvirt.
if virsh -q net-info default >/dev/null 2>&1; then
  virsh -q net-autostart default >/dev/null 2>&1 || true
  NETINFO="$(virsh -q net-info default)"
  if ! [[ "$NETINFO" =~ Active:[[:space:]]+yes ]]; then
    if virsh -q net-start default >/dev/null 2>&1; then ok "$(m 'NAT-Netzwerk default gestartet' 'NAT network default started')";
    else warn "$(m 'NAT-Netzwerk default konnte nicht starten (Adresskonflikt mit 192.168.122.0/24?). VMs können weiterhin an Bridges angebunden werden.' 'NAT network default failed to start (address conflict with 192.168.122.0/24?). VMs can still use bridges.')"; fi
  else
    ok "$(m 'NAT-Netzwerk default aktiv' 'NAT network default active')"
  fi
else
  warn "$(m 'Kein libvirt-Netzwerk default vorhanden' 'No libvirt network default present')"
fi

########################################################################
step "$(m 'Konfiguration und TLS' 'Configuration and TLS')"
CONF=/etc/hostmanaged/hostmanaged.conf
if [ ! -f "$CONF" ]; then
  cat > "$CONF" <<EOF
# Host-managed configuration; all keys: deploy/hostmanaged.conf.example
listen = :$PORT
tls_cert = /etc/hostmanaged/tls/server.crt
tls_key = /etc/hostmanaged/tls/server.key
data_dir = /var/lib/hostmanaged
agent_socket = /run/hostmanaged/agent.sock
agent_group = hostmanaged
allowed_user = hostmanaged
allow_software_emulation = $SOFTEMU
default_language = $LANGSEL
product_name = Host-managed
EOF
  chown root:hostmanaged "$CONF"; chmod 0640 "$CONF"
  ok "$(m 'Konfiguration geschrieben' 'Configuration written'): $CONF"
else
  ok "$(m 'Vorhandene Konfiguration beibehalten' 'Existing configuration kept'): $CONF"
fi

CRT=/etc/hostmanaged/tls/server.crt
KEY=/etc/hostmanaged/tls/server.key
if [ ! -f "$CRT" ] || [ ! -f "$KEY" ]; then
  HOST_FQDN="$(hostname -f 2>/dev/null || hostname)"
  SAN="DNS:$(hostname),DNS:$HOST_FQDN,DNS:localhost,IP:127.0.0.1,IP:::1"
  for a in $(ip -o addr show scope global | awk '{print $4}' | cut -d/ -f1); do SAN="$SAN,IP:$a"; done
  umask 077
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 825 \
    -subj "/CN=$HOST_FQDN/O=Host-managed" -addext "subjectAltName=$SAN" \
    -addext "keyUsage=digitalSignature" -addext "extendedKeyUsage=serverAuth" \
    -keyout "$KEY" -out "$CRT" >/dev/null 2>&1
  umask 022
  chown root:hostmanaged "$KEY" "$CRT"; chmod 0640 "$KEY"; chmod 0644 "$CRT"
  ok "$(m 'Selbstsigniertes Zertifikat erzeugt' 'Self-signed certificate created') (SAN: $SAN)"
else
  ok "$(m 'Vorhandenes Zertifikat beibehalten' 'Existing certificate kept')"
fi
FINGERPRINT="$(openssl x509 -in "$CRT" -noout -fingerprint -sha256 | cut -d= -f2)"

########################################################################
step "$(m 'Dienste' 'Services')"
UPGRADE=0
if [ -x /usr/local/sbin/hostmanaged ]; then
  UPGRADE=1
  OLD_VERSION="$(/usr/local/sbin/hostmanaged version 2>/dev/null || echo unknown)"
  if [ -f /var/lib/hostmanaged/hostmanaged.db ]; then
    BK="$(/usr/local/sbin/hostmanaged backup -config "$CONF" 2>/dev/null || true)"
    [ -n "$BK" ] && ok "$(m 'Sicherung vor dem Update' 'Backup before upgrade'): $BK"
  fi
  # Stopping the control plane and agent does not affect running VMs/containers.
  systemctl stop hostmanaged.service hostmanaged-agent.service 2>/dev/null || true
  ok "$(m 'Update' 'Upgrade'): $OLD_VERSION → $NEW_VERSION"
fi
install -m 0755 "$BIN_DIR/hostmanaged" /usr/local/sbin/hostmanaged
install -m 0755 "$BIN_DIR/hostctl" /usr/local/bin/hostctl
install -m 0644 "$SRC_DIR/deploy/systemd/hostmanaged-agent.service" /etc/systemd/system/hostmanaged-agent.service
install -m 0644 "$SRC_DIR/deploy/systemd/hostmanaged.service" /etc/systemd/system/hostmanaged.service
systemctl daemon-reload

if [ ! -f /var/lib/hostmanaged/hostmanaged.db ]; then
  # First start: create the database as the service user.
  (umask 027; runuser -u hostmanaged -- /usr/local/sbin/hostmanaged admin create -config "$CONF" --if-no-users --username "$ADMIN" --language "$LANGSEL") > /root/.hostmanaged-admin.tmp
  chmod 0600 /root/.hostmanaged-admin.tmp
  CREATED_ADMIN=1
fi

systemctl enable --now hostmanaged-agent.service hostmanaged.service >/dev/null 2>&1
for i in $(seq 1 30); do
  if curl -fsk "https://127.0.0.1:$PORT/api/v1/health" >/dev/null 2>&1 || wget -q --no-check-certificate -O /dev/null "https://127.0.0.1:$PORT/api/v1/health" 2>/dev/null; then HEALTHY=1; break; fi
  sleep 1
done
systemctl is-active --quiet hostmanaged-agent || die "hostmanaged-agent $(m 'startet nicht' 'does not start'): journalctl -u hostmanaged-agent"
[ "${HEALTHY:-0}" = 1 ] || die "hostmanaged $(m 'antwortet nicht' 'does not respond'): journalctl -u hostmanaged"
ok "hostmanaged-agent, hostmanaged $(m 'laufen' 'running')"

########################################################################
step "$(m 'Fertig' 'Done')"
RULESET="$(nft list ruleset 2>/dev/null || true)"
if [[ "$RULESET" == *"policy drop"* ]]; then
  warn "$(m 'Eine Firewall mit Standard-Policy drop ist aktiv. Port' 'A firewall with default drop policy is active. Allow TCP port') $PORT $(m 'muss erlaubt werden; das Installationsprogramm ändert keine Firewall-Regeln.' '; the installer does not change firewall rules.')"
fi
URL_HOST="${MGMT_IP:-$(hostname -f)}"
{
  echo
  echo "  $(m 'Weboberfläche' 'Web interface'):  https://$URL_HOST:$PORT"
  [ -n "${MGMT_IP6:-}" ] && echo "                   https://[$MGMT_IP6]:$PORT"
  echo "  $(m 'Zertifikat (SHA-256)' 'Certificate (SHA-256)'): $FINGERPRINT"
  echo "  $(m 'Vergleichen Sie den Fingerprint mit der Browser-Warnung, bevor Sie das Zertifikat akzeptieren.' 'Compare the fingerprint with the browser warning before accepting the certificate.')"
} | tee -a "$LOG" >&3

if [ "${CREATED_ADMIN:-0}" = 1 ]; then
  # Shown on the terminal only; not written to the install log.
  echo >&3
  sed 's/^/  /' /root/.hostmanaged-admin.tmp >&3
  mv /root/.hostmanaged-admin.tmp /root/hostmanaged-initial-admin.txt
  echo "  $(m 'Zugangsdaten zusätzlich in /root/hostmanaged-initial-admin.txt (nur root). Nach der ersten Anmeldung Passwort ändern und Datei löschen.' 'Credentials also stored in /root/hostmanaged-initial-admin.txt (root only). Change the password after the first login and delete the file.')" >&3
fi
echo "  $(m 'Protokoll' 'Log'): $LOG" >&3
