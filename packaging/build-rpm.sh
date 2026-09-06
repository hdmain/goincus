#!/usr/bin/env bash
# Build a goincus .rpm from a prebuilt static binary (requires rpmbuild).
set -euo pipefail

VERSION="${1:?version}"
ARCH="${2:?arch x86_64|aarch64}"
BINARY="${3:?path to goincus binary}"
OUT_DIR="${4:-dist}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PKG_NAME="goincus"
SAFE_VERSION="${VERSION//-/_}"

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

mkdir -p "${WORK}"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
install -m 0755 "${BINARY}" "${WORK}/SOURCES/goincus"
cp "${ROOT}/configs/config.yaml" "${WORK}/SOURCES/config.yaml.example"
cp "${ROOT}/deploy/systemd/goincus.service" "${WORK}/SOURCES/"
cp "${ROOT}/migrations/"*.sql "${WORK}/SOURCES/"

cat > "${WORK}/SPECS/goincus.spec" <<EOF
Name:           ${PKG_NAME}
Version:        ${SAFE_VERSION}
Release:        1%{?dist}
Summary:        Incus NAT VPS provisioning REST API
License:        Apache-2.0
URL:            https://github.com/hdmain/goincus
BuildArch:      ${ARCH}
Requires:       systemd

%description
goincus provisions and lifecycle-manages isolated LXC containers as NAT VPS
instances using Incus, with dynamic host port mapping and resource limits.

%install
mkdir -p %{buildroot}/usr/local/bin
mkdir -p %{buildroot}/etc/goincus
mkdir -p %{buildroot}/usr/share/goincus/migrations
mkdir -p %{buildroot}/etc/systemd/system
mkdir -p %{buildroot}/opt/goincus
mkdir -p %{buildroot}/var/log/goincus
install -m 0755 %{_sourcedir}/goincus %{buildroot}/usr/local/bin/goincus
install -m 0644 %{_sourcedir}/config.yaml.example %{buildroot}/usr/share/goincus/config.yaml.example
install -m 0644 %{_sourcedir}/goincus.service %{buildroot}/etc/systemd/system/goincus.service
install -m 0644 %{_sourcedir}/*.sql %{buildroot}/usr/share/goincus/migrations/
printf '%s\\n' '# Live config is created by: sudo goincus init' > %{buildroot}/etc/goincus/.keep

%files
/usr/local/bin/goincus
/usr/share/goincus/config.yaml.example
/etc/goincus/.keep
/etc/systemd/system/goincus.service
/usr/share/goincus/migrations/*
%dir /etc/goincus
%dir /opt/goincus
%dir /var/log/goincus

%post
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi
if [ ! -f /etc/goincus/config.yaml ]; then
  echo "goincus: no config yet. Run: sudo goincus init && sudo systemctl enable --now goincus"
else
  if command -v systemctl >/dev/null 2>&1 && systemctl is-enabled goincus >/dev/null 2>&1; then
    systemctl try-restart goincus >/dev/null 2>&1 || true
  fi
  echo "goincus upgraded. Config preserved at /etc/goincus/config.yaml"
fi

%preun
if [ "\$1" = "0" ] && command -v systemctl >/dev/null 2>&1; then
  systemctl stop goincus >/dev/null 2>&1 || true
  systemctl disable goincus >/dev/null 2>&1 || true
fi
EOF

rpmbuild \
  --define "_topdir ${WORK}" \
  --define "_rpmdir ${WORK}/RPMS" \
  -bb "${WORK}/SPECS/goincus.spec"

mkdir -p "${OUT_DIR}"
find "${WORK}/RPMS" -name '*.rpm' -exec cp {} "${OUT_DIR}/" \;
ls -la "${OUT_DIR}"/*.rpm
