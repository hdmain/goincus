#!/usr/bin/env bash
# Verify NAT VPS isolation on a running instance (by name or UUID).
set -euo pipefail

API="${API:-http://127.0.0.1:9603}"
TARGET="${1:?usage: verify-isolation.sh <name-or-id>}"
KEY="$(awk '/api_keys:/{getline; gsub(/[" -]/,""); print; exit}' /etc/goincus/config.yaml)"

INST="$(curl -sS -H "X-API-Key: ${KEY}" "${API}/api/v1/instances/${TARGET}")"
echo "${INST}" > /tmp/verify-inst.json
python3 - <<'PY'
import json,sys
inst=json.load(open("/tmp/verify-inst.json"))
assert inst.get("status")=="running", inst
ports=inst.get("ports") or []
assert len(ports)>=20, f"expected >=20 ports, got {len(ports)}"
hs=sorted(p["host_port"] for p in ports)
assert hs[-1]-hs[0]+1 == len(hs), "ports not contiguous"
assert all(p["host_port"]==p["internal_port"] for p in ports), "not 1:1"
open("/tmp/v-incus","w").write(inst["incus_name"])
open("/tmp/v-ssh","w").write(str(hs[0]))
open("/tmp/v-pass","w").write(inst["root_password"])
open("/tmp/v-cpu","w").write(str(inst["cpu_cores"]))
open("/tmp/v-mem","w").write(str(inst["memory_mb"]))
open("/tmp/v-disk","w").write(str(inst["storage_gb"]))
print(f"OK api: ports={len(ports)} range={hs[0]}-{hs[-1]} ssh={hs[0]}")
PY

INCUS=$(cat /tmp/v-incus)
SSH=$(cat /tmp/v-ssh)
PASS=$(cat /tmp/v-pass)
CPU=$(cat /tmp/v-cpu)
MEM=$(cat /tmp/v-mem)
DISK=$(cat /tmp/v-disk)

echo "== guest via incus exec =="
incus exec "${INCUS}" -- bash -lc "
set -e
NPROC=\$(nproc)
MEM_KB=\$(awk '/MemTotal/{print \$2}' /proc/meminfo)
DISK_G=\$(df -BG / | awk 'NR==2{gsub(/G/,\"\",\$2); print \$2}')
echo nproc=\$NPROC mem_kb=\$MEM_KB disk_g=\$DISK_G
test \"\$NPROC\" -eq ${CPU}
test \"\$MEM_KB\" -le \$(( ${MEM} * 1024 + 16384 ))
test \"\$DISK_G\" -le \$(( ${DISK} + 1 ))
# must NOT see host root disk device
if ls /dev/sda /dev/sda1 /dev/nvme0n1 2>/dev/null; then
  echo 'FAIL: host disk visible' >&2
  exit 1
fi
# must NOT see Incus guest API
if [ -e /dev/incus ]; then
  echo 'FAIL: /dev/incus exposed' >&2
  exit 1
fi
# nesting / privilege probes
if unshare -m true 2>/dev/null; then
  echo 'WARN: user can unshare mount ns (may be ok in unprivileged)'
fi
test ! -e /var/run/docker.sock
# cgroup memory
if [ -f /sys/fs/cgroup/memory.max ]; then
  MAX=\$(cat /sys/fs/cgroup/memory.max)
  echo cgroup.memory.max=\$MAX
  test \"\$MAX\" != 'max'
fi
echo GUEST_CHECKS_OK
"

echo "== ssh via public mapped port =="
apt-get install -y -qq sshpass >/dev/null 2>&1 || true
sshpass -p "${PASS}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  -o ConnectTimeout=10 -p "${SSH}" root@127.0.0.1 'echo SSH_OK; hostname; nproc; free -m | awk "NR==2{print}"; df -h / | awk "NR==2{print}"'

echo "== guest must not reach host management via bridge gateway =="
incus exec "${INCUS}" -- bash -lc '
set -e
GW=$(ip -4 route show default | awk "/default/{print \$3; exit}")
test -n "$GW"
# Internet + DNS still required after host INPUT hardening.
getent hosts 1.1.1.1 >/dev/null
ping -c1 -W2 1.1.1.1 >/dev/null
# Host SSH/API on the bridge gateway must fail (timeout/refused).
if timeout 3 bash -c "echo >/dev/tcp/$GW/22" 2>/dev/null; then
  echo "FAIL: guest can reach host SSH on $GW:22" >&2
  exit 1
fi
if timeout 3 bash -c "echo >/dev/tcp/$GW/9603" 2>/dev/null; then
  echo "FAIL: guest can reach host API on $GW:9603" >&2
  exit 1
fi
echo "GUEST_TO_HOST_BLOCKED gw=$GW"
'

echo "== guest must not reach Docker/private bridges via FORWARD/DNAT =="
incus exec "${INCUS}" -- bash -lc '
set -e
GW=$(ip -4 route show default | awk "/default/{print \$3; exit}")
# Published Docker ports on the host are DNATed into 172.16/12 and must not be reachable.
for ip in "$GW" 172.17.0.1 172.18.0.1; do
  for p in 80 443 2375 2376 8781 9000 3306; do
    if timeout 2 bash -c "echo >/dev/tcp/$ip/$p" 2>/dev/null; then
      echo "FAIL: guest can reach private/docker path $ip:$p" >&2
      exit 1
    fi
  done
done
getent hosts 1.1.1.1 >/dev/null
ping -c1 -W2 1.1.1.1 >/dev/null
echo "GUEST_TO_PRIVATE_BLOCKED"
'

echo "== host must not publish ports outside the block =="
# pick a port just outside the block
OUT=$((SSH + 20))
if ss -lnt | awk '{print $4}' | grep -E ":${OUT}\$" >/dev/null; then
  # only fail if it belongs to this instance's proxy
  if incus config device show "${INCUS}" | grep -q "proxy-${OUT}"; then
    echo "FAIL: unexpected proxy ${OUT}" >&2
    exit 1
  fi
fi
PROXY_N=$(incus config device show "${INCUS}" | grep -c 'type: proxy' || true)
test "${PROXY_N}" -eq 20
echo "PROXY_N=${PROXY_N} OK"

echo "== security flags =="
incus config get "${INCUS}" security.privileged | grep -qx false
incus config get "${INCUS}" security.nesting | grep -qx false
incus config get "${INCUS}" security.guestapi | grep -qx false
incus config get "${INCUS}" security.idmap.isolated | grep -qx true
echo ALL_ISOLATION_CHECKS_PASSED
