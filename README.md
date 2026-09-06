# goincus

REST API for provisioning and lifecycle-managing isolated LXC containers as NAT VPS instances with [Incus](https://linuxcontainers.org/incus/).

## Install

### Debian / Ubuntu (apt)

```bash
echo "deb [trusted=yes lang=none] https://hdmain.github.io/goincus ./" \
  | sudo tee /etc/apt/sources.list.d/goincus.list
sudo apt update
sudo apt install goincus
sudo goincus init
sudo systemctl enable --now goincus
```

### Fedora / AlmaLinux / CentOS (dnf)

```bash
sudo tee /etc/yum.repos.d/goincus.repo <<'EOF'
[goincus]
name=goincus
baseurl=https://hdmain.github.io/goincus/rpm
enabled=1
gpgcheck=0
EOF
sudo dnf install goincus
sudo goincus init
sudo systemctl enable --now goincus
```

Packages are published from CI to [hdmain.github.io/goincus](https://hdmain.github.io/goincus/).

## Ports

| Service | Bind |
|---------|------|
| PostgreSQL | `127.0.0.1:9601` |
| Redis | `127.0.0.1:9602` (or existing Redis, any port) |
| HTTP API | `0.0.0.0:9603` |

## Quick start (from source)

```bash
go build -o goincus ./cmd/goincus
sudo ./goincus init
sudo systemctl enable --now goincus
```

`goincus init` will:

1. Install **PostgreSQL**, **Redis** (only if none exists), and **Incus**
2. If Redis is already running or configured locally, **reuse it on its current port**
3. Bind PostgreSQL to `127.0.0.1:9601` and create the `goincus` database role
4. Write `/etc/goincus/config.yaml` with generated DB password and API keys
5. Install the systemd unit and SQL migrations

Package upgrades do **not** overwrite `/etc/goincus/config.yaml` (template lives at `/usr/share/goincus/config.yaml.example`).

Disk size (`storage_gb`) needs an **LVM or ZFS** Incus pool so guest `df` shows ~10G.
goincus **installs `lvm2` automatically** (on `serve` / `goincus-net`) and creates
`goincus-lvm` if missing — then persists `storage_pool` in config. Recreate old VPS
instances after the pool exists; dir/btrfs guests always show the host disk in `df`.

## Guest isolation

Each VPS is an **unprivileged** Incus container with:

- no privileged mode, no nesting (cannot run nested containers/Docker that need it)
- isolated UID/GID map per instance (`security.idmap.isolated`)
- Incus guest API disabled (`/dev/incus` not exposed)
- default seccomp deny + no syscall intercept helpers
- NIC MAC/IPv4/IPv6 filtering and bridge port isolation (no guest↔guest)
- host sysctl hardening (`/etc/sysctl.d/99-goincus-isolation.conf`)

Residual risk: all containers share the host kernel — a kernel 0-day can still escape. For stronger isolation use VMs/microVMs.

## SSH access

Each VPS gets a contiguous block of host ports (default **20**, e.g. `20000–20019`),
mapped **1:1** into the guest (`publicIP:N` → container `:N`). OpenSSH listens on the
**first** port of that block (not 22).

`root_password` is returned by **create / get / repair** — not by list.

```bash
# ports[0].host_port is the SSH port (and guest listen port)
ssh root@HOST -p SSH_PORT
```

All endpoints except `/healthz` and `/api/v1/health` require:

```http
Authorization: Bearer <api_key>
```

or `X-API-Key: <api_key>`.

## Production hardening

- Config must be `chmod 600 /etc/goincus/config.yaml` (enforced at serve start)
- API key compare is constant-time; failed auth is rate-limited per IP
- `GET /instances` omits `root_password` (fetch one instance to read it)
- Guest sshd: MaxAuthTries, LoginGraceTime, no empty passwords, no X11
- Host NAT/FORWARD persisted via `goincus-net.service` (Docker-safe)
- Put TLS in front (nginx/caddy) if the API is reachable from the internet
- Open host firewall for mapped VPS ports, e.g. `20000-29999/tcp`

```bash
sudo systemctl enable --now goincus-net goincus
sudo ufw allow 9603/tcp
sudo ufw allow 20000:29999/tcp
```

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

## License

Apache-2.0
