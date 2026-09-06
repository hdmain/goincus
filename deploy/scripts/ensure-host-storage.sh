#!/bin/sh
# Auto-install LVM (and best-effort ZFS) and create an Incus pool so guest df
# shows the VPS disk quota instead of the host disk. Idempotent.
set -eu

POOL_LVM="${GOINCUS_STORAGE_POOL:-goincus-lvm}"
POOL_ZFS="${GOINCUS_STORAGE_POOL_ZFS:-goincus-zfs}"
SIZE="${GOINCUS_STORAGE_SIZE:-200GiB}"

log() { echo "goincus-storage: $*"; }

have_bin() { command -v "$1" >/dev/null 2>&1; }

install_pkgs() {
  if have_bin lvcreate && have_bin vgcreate; then
    return 0
  fi
  if [ "$(id -u)" -ne 0 ]; then
    log "not root; skip package install"
    return 1
  fi
  export DEBIAN_FRONTEND=noninteractive
  if have_bin apt-get; then
    log "installing lvm2 thin-provisioning-tools"
    apt-get update -y >/dev/null 2>&1 || true
    apt-get install -y lvm2 thin-provisioning-tools
    apt-get install -y zfsutils-linux >/dev/null 2>&1 || true
  elif have_bin dnf; then
    log "installing lvm2"
    dnf install -y lvm2
  elif have_bin yum; then
    log "installing lvm2"
    yum install -y lvm2
  else
    log "no package manager found"
    return 1
  fi
}

pool_driver() {
  # prints driver or empty
  incus storage show "$1" 2>/dev/null | awk -F': ' '/^driver:/{print $2; exit}'
}

is_good_driver() {
  case "$1" in
    zfs|lvm|lvmcluster|ceph) return 0 ;;
    *) return 1 ;;
  esac
}

ensure_pool() {
  name="$1"
  driver="$2"
  cur="$(pool_driver "$name" || true)"
  if [ -n "$cur" ]; then
    if is_good_driver "$cur"; then
      log "pool $name already ok ($cur)"
      return 0
    fi
    log "pool $name exists as $cur (not df-isolated); trying another name"
    return 1
  fi
  if ! have_bin incus; then
    log "incus not in PATH"
    return 1
  fi
  case "$driver" in
    lvm)
      have_bin lvcreate || return 1
      ;;
    zfs)
      have_bin zpool || return 1
      ;;
  esac
  log "creating pool $name ($driver size=$SIZE)"
  incus storage create "$name" "$driver" "size=$SIZE"
}

install_pkgs || true

# Prefer existing good pools.
for name in goincus "$POOL_LVM" "$POOL_ZFS"; do
  d="$(pool_driver "$name" || true)"
  if is_good_driver "$d"; then
    log "ready: $name ($d)"
    exit 0
  fi
done

# Create LVM first (auto-installed), then ZFS if tools exist.
ensure_pool "$POOL_LVM" lvm && exit 0
ensure_pool goincus lvm && exit 0
ensure_pool "$POOL_ZFS" zfs && exit 0
ensure_pool goincus zfs && exit 0

log "failed to create zfs/lvm pool — guest df will show host disk until fixed"
exit 1
