#!/bin/sh
# Persistable host egress for goincus Incus bridge (survives reboot; Docker-safe).
set -eu
BR="${GOINCUS_BRIDGE:-incusbr0}"
SUBNET="${GOINCUS_SUBNET:-10.72.160.0/24}"

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
exit 0
