# goincus

REST API for provisioning and lifecycle-managing isolated LXC containers as NAT VPS instances with [Incus](https://linuxcontainers.org/incus/).

## Ports

| Service | Bind |
|---------|------|
| PostgreSQL | `127.0.0.1:9601` |
| Redis | `127.0.0.1:9602` |
| HTTP API | `0.0.0.0:9603` |

## Quick start (Linux)

```bash
# Build
go build -o goincus ./cmd/goincus

# Install PostgreSQL, Redis, Incus; generate config, passwords, and API keys
sudo ./goincus init

# Start
sudo systemctl enable --now goincus
```

`goincus init` will:

1. Install **PostgreSQL**, **Redis** (only if none exists), and **Incus**
2. If Redis is already running or configured locally, **reuse it on its current port** (any port)
3. Bind PostgreSQL to `127.0.0.1:9601` and create the `goincus` database role
4. Write `/etc/goincus/config.yaml` with generated DB password and API keys (Redis password taken from the existing instance when reused)
5. Install the systemd unit and SQL migrations

## API auth

All endpoints except `/healthz` and `/api/v1/health` require:

```http
Authorization: Bearer <api_key>
```

or `X-API-Key: <api_key>`.

## Examples

Python scripts in [`example/`](example/) (stdlib only):

```bash
python example/health.py http://127.0.0.1:9603
python example/create.py http://127.0.0.1:9603 gic_xxx web-1
python example/list.py http://127.0.0.1:9603 gic_xxx
python example/get.py http://127.0.0.1:9603 gic_xxx <id>
python example/start.py http://127.0.0.1:9603 gic_xxx <id>
python example/stop.py http://127.0.0.1:9603 gic_xxx <id>
python example/restart.py http://127.0.0.1:9603 gic_xxx <id>
python example/repair.py http://127.0.0.1:9603 gic_xxx <id>
python example/add_port.py http://127.0.0.1:9603 gic_xxx <id> 443
python example/remove_port.py http://127.0.0.1:9603 gic_xxx <id> <port-id>
python example/delete.py http://127.0.0.1:9603 gic_xxx <id>
```

Or with curl:

```bash
curl -s -H "Authorization: Bearer $GOINCUS_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"web-1","cpu_cores":1,"memory_mb":512,"storage_gb":10}' \
  http://127.0.0.1:9603/api/v1/instances
```

## License

Apache-2.0
