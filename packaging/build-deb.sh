#!/usr/bin/env bash
# Build a goincus .deb from a prebuilt static binary.
set -euo pipefail

VERSION="${1:?version}"
ARCH="${2:?arch amd64|arm64}"
BINARY="${3:?path to goincus binary}"
OUT_DIR="${4:-dist}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PKG_NAME="goincus"
PKG_DIR="${OUT_DIR}/${PKG_NAME}_${VERSION}_${ARCH}"

rm -rf "${PKG_DIR}"
mkdir -p \
  "${PKG_DIR}/DEBIAN" \
  "${PKG_DIR}/usr/local/bin" \
  "${PKG_DIR}/etc/goincus" \
  "${PKG_DIR}/usr/share/goincus/migrations" \
  "${PKG_DIR}/etc/systemd/system" \
  "${PKG_DIR}/opt/goincus" \
  "${PKG_DIR}/var/log/goincus"

install -m 0755 "${BINARY}" "${PKG_DIR}/usr/local/bin/goincus"
# Never ship live /etc/goincus/config.yaml — apt upgrades would overwrite secrets.
install -m 0644 "${ROOT}/configs/config.yaml" "${PKG_DIR}/usr/share/goincus/config.yaml.example"
install -m 0644 "${ROOT}/deploy/systemd/goincus.service" "${PKG_DIR}/etc/systemd/system/goincus.service"
install -m 0644 "${ROOT}/deploy/systemd/goincus-net.service" "${PKG_DIR}/etc/systemd/system/goincus-net.service"
mkdir -p "${PKG_DIR}/usr/local/libexec/goincus"
install -m 0755 "${ROOT}/deploy/scripts/ensure-host-nat.sh" "${PKG_DIR}/usr/local/libexec/goincus/ensure-host-nat.sh"
install -m 0755 "${ROOT}/deploy/scripts/ensure-host-storage.sh" "${PKG_DIR}/usr/local/libexec/goincus/ensure-host-storage.sh"
install -m 0644 "${ROOT}/migrations/"*.sql "${PKG_DIR}/usr/share/goincus/migrations/"

# Keep an empty config dir owned by root.
cat > "${PKG_DIR}/etc/goincus/.keep" <<'EOF'
# Live config is created by: sudo goincus init
# Example template: /usr/share/goincus/config.yaml.example
EOF

cat > "${PKG_DIR}/DEBIAN/control" <<EOF
Package: ${PKG_NAME}
Version: ${VERSION}
Section: admin
Priority: optional
Architecture: ${ARCH}
Maintainer: hdmain <noreply@github.com>
Depends: systemd
Recommends: curl, ca-certificates, lvm2, thin-provisioning-tools
Homepage: https://github.com/hdmain/goincus
Description: Incus NAT VPS provisioning REST API
 goincus provisions and lifecycle-manages isolated LXC containers as NAT VPS
 instances using Incus, with dynamic host port mapping and resource limits.
EOF

cat > "${PKG_DIR}/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
case "$1" in
  configure)
    if command -v systemctl >/dev/null 2>&1; then
      systemctl daemon-reload || true
    fi
    if [ ! -f /etc/goincus/config.yaml ]; then
      echo "goincus: no config yet. Run: sudo goincus init && sudo systemctl enable --now goincus-net goincus"
    else
      # Upgrade path: keep secrets; bounce service if it was enabled.
      if command -v systemctl >/dev/null 2>&1; then
        systemctl enable goincus-net >/dev/null 2>&1 || true
        systemctl start goincus-net >/dev/null 2>&1 || true
        if systemctl is-enabled goincus >/dev/null 2>&1; then
          systemctl try-restart goincus >/dev/null 2>&1 || true
        fi
      fi
      echo "goincus upgraded. Config preserved at /etc/goincus/config.yaml"
    fi
    ;;
esac
EOF
chmod 0755 "${PKG_DIR}/DEBIAN/postinst"

cat > "${PKG_DIR}/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
# Only on full remove — never disable/stop permanently during apt upgrade.
if [ "$1" = "remove" ] && command -v systemctl >/dev/null 2>&1; then
  systemctl stop goincus >/dev/null 2>&1 || true
  systemctl disable goincus >/dev/null 2>&1 || true
fi
EOF
chmod 0755 "${PKG_DIR}/DEBIAN/prerm"

mkdir -p "${OUT_DIR}"
dpkg-deb --root-owner-group --build "${PKG_DIR}" "${OUT_DIR}/${PKG_NAME}_${VERSION}_${ARCH}.deb"
echo "built ${OUT_DIR}/${PKG_NAME}_${VERSION}_${ARCH}.deb"
