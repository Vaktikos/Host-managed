#!/usr/bin/env bash
# Removes Host-managed services and binaries.
#
# Running VMs and containers are NOT stopped or deleted: they are managed by
# libvirt and LXC and keep running without Host-managed.
#
#   --purge         also delete /etc/hostmanaged and /var/lib/hostmanaged
#                   (database, certificates, backups) – VM disk images in
#                   /var/lib/hostmanaged-storage are kept unless
#   --purge-images  is given as well (requires typing "delete images").
set -euo pipefail
PURGE=0
PURGE_IMAGES=0
for a in "$@"; do
  case "$a" in
    --purge) PURGE=1 ;;
    --purge-images) PURGE_IMAGES=1 ;;
    *) echo "unknown option: $a" >&2; exit 2 ;;
  esac
done
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }

if [ "$PURGE" = 1 ] && [ -x /usr/local/sbin/hostmanaged ] && [ -f /var/lib/hostmanaged/hostmanaged.db ]; then
  # Consistent snapshot of the control plane before anything is removed.
  /usr/local/sbin/hostmanaged backup -output /root/hostmanaged-final-backup.tar.gz && \
    echo "Final backup: /root/hostmanaged-final-backup.tar.gz"
fi
systemctl disable --now hostmanaged.service hostmanaged-agent.service 2>/dev/null || true
rm -f /etc/systemd/system/hostmanaged.service /etc/systemd/system/hostmanaged-agent.service
systemctl daemon-reload
rm -f /usr/local/sbin/hostmanaged /usr/local/bin/hostctl
echo "Services and binaries removed. VMs and containers were not touched."

if [ "$PURGE" = 1 ]; then
  rm -rf /etc/hostmanaged
  rm -rf /var/lib/hostmanaged
  echo "Configuration and control plane data removed."
fi

if [ "$PURGE_IMAGES" = 1 ]; then
  echo "This deletes ALL files in /var/lib/hostmanaged-storage (images and iso),"
  echo "including disks of VMs that still exist in libvirt."
  read -r -p 'Type "delete images" to continue: ' ans
  if [ "$ans" = "delete images" ]; then
    virsh pool-destroy local 2>/dev/null || true; virsh pool-undefine local 2>/dev/null || true
    virsh pool-destroy iso 2>/dev/null || true; virsh pool-undefine iso 2>/dev/null || true
    rm -rf /var/lib/hostmanaged-storage
    echo "Images removed."
  else
    echo "Images kept."
  fi
fi
