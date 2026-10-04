#!/usr/bin/env bash
set -euo pipefail
API=http://127.0.0.1:9603
KEY=$(awk '/api_keys:/{getline; gsub(/[" -]/,""); print; exit}' /etc/goincus/config.yaml)
NAME=nat-fresh

# delete prior
for id in $(curl -sS -H "X-API-Key: $KEY" "$API/api/v1/instances/" \
  | python3 -c 'import json,sys; print("\n".join(i["id"] for i in json.load(sys.stdin) if i.get("name")=="'"$NAME"'"))'); do
  curl -sS -X DELETE -H "X-API-Key: $KEY" "$API/api/v1/instances/$id" >/dev/null || true
done
sleep 1

CREATE=$(curl -sS -X POST -H "X-API-Key: $KEY" -H "Content-Type: application/json" \
  -d "{\"name\":\"$NAME\",\"cpu_cores\":1,\"memory_mb\":512,\"storage_gb\":5}" \
  "$API/api/v1/instances/")
ID=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$CREATE")
echo "ID=$ID"

# While creating, status must stay creating (not premature running).
saw_creating=0
for i in $(seq 1 120); do
  RESP=$(curl -sS -H "X-API-Key: $KEY" "$API/api/v1/instances/$ID")
  STATUS=$(python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])' <<<"$RESP")
  PORTS=$(python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("ports") or []))' <<<"$RESP")
  echo "t=$((i*3))s status=$STATUS ports=$PORTS"
  if [[ "$STATUS" == "creating" ]]; then saw_creating=1; fi
  if [[ "$STATUS" == "running" ]]; then
    if [[ "$PORTS" -lt 20 ]]; then
      echo "FAIL: running with incomplete ports" >&2
      exit 1
    fi
    break
  fi
  if [[ "$STATUS" == "error" ]]; then
    echo "$RESP" >&2
    exit 1
  fi
  sleep 3
done

test "$saw_creating" -eq 1
test "$STATUS" = "running"
tr -d '\r' < /tmp/verify-isolation.sh > /tmp/verify-isolation.lf.sh
bash /tmp/verify-isolation.lf.sh "$NAME"
echo FRESH_CREATE_OK
