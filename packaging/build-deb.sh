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
install -m 0644 "${ROOT}/configs/config.yaml" "${PKG_DIR}/etc/goincus/config.yaml"
install -m 0644 "${ROOT}/deploy/systemd/goincus.service" "${PKG_DIR}/etc/systemd/system/goincus.service"
install -m 0644 "${ROOT}/migrations/"*.sql "${PKG_DIR}/usr/share/goincus/migrations/"

cat > "${PKG_DIR}/DEBIAN/control" <<EOF
Package: ${PKG_NAME}
Version: ${VERSION}
Section: admin
Priority: optional
Architecture: ${ARCH}
Maintainer: hdmain <noreply@github.com>
Depends: systemd
Recommends: curl, ca-certificates
Homepage: https://github.com/hdmain/goincus
Description: Incus NAT VPS provisioning REST API
 goincus provisions and lifecycle-manages isolated LXC containers as NAT VPS
 instances using Incus, with dynamic host port mapping and resource limits.
EOF

cat > "${PKG_DIR}/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi
echo "goincus installed. Next: sudo goincus init && sudo systemctl enable --now goincus"
EOF
chmod 0755 "${PKG_DIR}/DEBIAN/postinst"

cat > "${PKG_DIR}/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop goincus >/dev/null 2>&1 || true
  systemctl disable goincus >/dev/null 2>&1 || true
fi
EOF
chmod 0755 "${PKG_DIR}/DEBIAN/prerm"

mkdir -p "${OUT_DIR}"
dpkg-deb --root-owner-group --build "${PKG_DIR}" "${OUT_DIR}/${PKG_NAME}_${VERSION}_${ARCH}.deb"
echo "built ${OUT_DIR}/${PKG_NAME}_${VERSION}_${ARCH}.deb"
