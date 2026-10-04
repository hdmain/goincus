#!/bin/sh
# Persistable host egress for goincus Incus bridge (survives reboot; Docker-safe).
# Portable: reads bridge/subnet/ports from env or /etc/goincus/config.yaml when present.
set -eu

CFG="${GOINCUS_CONFIG:-/etc/goincus/config.yaml}"

# Optional YAML helpers (no python/yq required).
yaml_get() {
  # usage: yaml_get <parent_key> <child_key>
  # returns first matching "child_key: value" under a section (best-effort).
  parent="$1"
  child="$2"
  [ -f "$CFG" ] || return 0
  awk -v p="$parent" -v c="$child" '
    $0 ~ "^"p":" { insec=1; next }
    insec && /^[^[:space:]#]/ { insec=0 }
    insec && $1 == c":" {
      v=$2
      gsub(/"/, "", v)
      print v
      exit
    }
  ' "$CFG"
}

BR="${GOINCUS_BRIDGE:-$(yaml_get incus network)}"
BR="${BR:-incusbr0}"

SUBNET="${GOINCUS_SUBNET:-}"
if [ -z "$SUBNET" ]; then
  # Derive /24 from configured bridge when possible; else default lab CIDR.
  SUBNET="10.72.160.0/24"
fi

PORT_START="${GOINCUS_PORT_START:-$(yaml_get ports host_range_start)}"
PORT_END="${GOINCUS_PORT_END:-$(yaml_get ports host_range_end)}"
PORT_START="${PORT_START:-20000}"
PORT_END="${PORT_END:-29999}"

API_PORT="${GOINCUS_API_PORT:-$(yaml_get server port)}"
API_PORT="${API_PORT:-9603}"

# Incus security.ipv4/ipv6_filtering needs bridge netfilter.
mkdir -p /etc/modules-load.d
printf 'br_netfilter\n' > /etc/modules-load.d/goincus-br-netfilter.conf
modprobe br_netfilter 2>/dev/null || true

sysctl -w net.ipv4.ip_forward=1 >/dev/null
sysctl -w net.bridge.bridge-nf-call-iptables=1 >/dev/null 2>&1 || true
sysctl -w net.bridge.bridge-nf-call-ip6tables=1 >/dev/null 2>&1 || true
sysctl -w net.bridge.bridge-nf-call-arptables=1 >/dev/null 2>&1 || true

mkdir -p /etc/sysctl.d
printf 'net.ipv4.ip_forward=1\n' > /etc/sysctl.d/99-goincus-forward.conf
printf '%s\n' \
  'net.bridge.bridge-nf-call-iptables = 1' \
  'net.bridge.bridge-nf-call-ip6tables = 1' \
  'net.bridge.bridge-nf-call-arptables = 1' \
  > /etc/sysctl.d/99-goincus-br-netfilter.conf

IPT="$(command -v iptables-nft 2>/dev/null || command -v iptables || true)"
[ -n "$IPT" ] || exit 0

$IPT -t nat -C POSTROUTING -s "$SUBNET" ! -d "$SUBNET" -j MASQUERADE 2>/dev/null \
  || $IPT -t nat -A POSTROUTING -s "$SUBNET" ! -d "$SUBNET" -j MASQUERADE
$IPT -C FORWARD -i "$BR" -j ACCEPT 2>/dev/null || $IPT -I FORWARD 1 -i "$BR" -j ACCEPT
$IPT -C FORWARD -o "$BR" -j ACCEPT 2>/dev/null || $IPT -I FORWARD 1 -o "$BR" -j ACCEPT
if $IPT -L DOCKER-USER -n >/dev/null 2>&1; then
  $IPT -C DOCKER-USER -i "$BR" -j ACCEPT 2>/dev/null || $IPT -I DOCKER-USER 1 -i "$BR" -j ACCEPT
  $IPT -C DOCKER-USER -o "$BR" -j ACCEPT 2>/dev/null || $IPT -I DOCKER-USER 1 -o "$BR" -j ACCEPT
fi

# Open published NAT VPS port range (+ API) when UFW is active (any host).
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi 'Status: active'; then
  ufw allow "${PORT_START}:${PORT_END}/tcp" comment 'goincus NAT VPS ports' >/dev/null 2>&1 || true
  ufw allow "${API_PORT}/tcp" comment 'goincus API' >/dev/null 2>&1 || true
fi

# firewalld (RHEL/Fedora/Alma) — best-effort, same idea.
if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state 2>/dev/null | grep -qi running; then
  firewall-cmd --permanent --add-port="${PORT_START}-${PORT_END}/tcp" >/dev/null 2>&1 || true
  firewall-cmd --permanent --add-port="${API_PORT}/tcp" >/dev/null 2>&1 || true
  firewall-cmd --reload >/dev/null 2>&1 || true
fi

exit 0
