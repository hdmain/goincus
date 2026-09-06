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

1. Install **PostgreSQL**, **Redis**, and **Incus** (required)
2. Bind them to the ports above and create the `goincus` database role
3. Write `/etc/goincus/config.yaml` with generated DB/Redis passwords and API keys
4. Install the systemd unit and SQL migrations

## API auth

All endpoints except `/healthz` and `/api/v1/health` require:

```http
Authorization: Bearer <api_key>
```

or `X-API-Key: <api_key>`.

## Example

```bash
curl -s -H "Authorization: Bearer $GOINCUS_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"web-1","cpu_cores":1,"memory_mb":512,"storage_gb":10}' \
  http://127.0.0.1:9603/api/v1/instances
```

## License

Apache-2.0
