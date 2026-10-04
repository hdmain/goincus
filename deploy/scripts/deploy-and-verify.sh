#!/usr/bin/env bash
set -euo pipefail

install -m 0755 /tmp/goincus-new /usr/local/bin/goincus
install -m 0644 /tmp/goincus.service /etc/systemd/system/goincus.service
systemctl daemon-reload
systemctl restart goincus
sleep 2
systemctl is-active goincus
curl -sS http://127.0.0.1:9603/healthz; echo
/usr/local/bin/goincus version

KEY=$(awk '/api_keys:/{getline; gsub(/[" -]/,""); print; exit}' /etc/goincus/config.yaml)

# Delete every API-visible instance except nat-demo (clears ghost rows + frees ports).
IDS=$(curl -sS -H "X-API-Key: $KEY" http://127.0.0.1:9603/api/v1/instances/ \
  | python3 -c 'import json,sys; print("\n".join(i["id"] for i in json.load(sys.stdin) if i.get("name")!="nat-demo"))')
for id in $IDS; do
  echo "cleanup $id"
  curl -sS -o /dev/null -w "%{http_code}\n" -X DELETE -H "X-API-Key: $KEY" "http://127.0.0.1:9603/api/v1/instances/$id" || true
done

tr -d '\r' < /tmp/verify-isolation.sh > /tmp/verify-isolation.lf.sh
chmod +x /tmp/verify-isolation.lf.sh
bash /tmp/verify-isolation.lf.sh nat-demo
