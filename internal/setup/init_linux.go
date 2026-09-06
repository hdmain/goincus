//go:build linux

package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hdmain/goincus/internal/config"
	"github.com/hdmain/goincus/internal/incusclient"
)

// Run installs dependencies, configures them, and writes a generated config.
func Run(opts Options) (*Result, error) {
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("goincus init must be run as root")
	}

	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = DefaultConfigPath
	}

	if !opts.Force {
		if _, err := os.Stat(cfgPath); err == nil {
			return nil, fmt.Errorf("config already exists at %s (use --force to overwrite)", cfgPath)
		}
	}

	cfg, err := config.GenerateInitialized()
	if err != nil {
		return nil, err
	}

	existingRedis, redisReused := detectExistingRedis()
	if redisReused {
		applyExistingRedis(cfg, existingRedis)
		fmt.Printf("==> Existing Redis detected at %s:%d (%s) — reusing it\n",
			existingRedis.Host, existingRedis.Port, existingRedis.Source)
	}

	if !opts.SkipInstall {
		fmt.Println("==> Installing required dependencies (PostgreSQL, Redis if missing, Incus)...")
		if err := installDependencies(!redisReused && !redisBinaryPresent()); err != nil {
			return nil, fmt.Errorf("dependency install failed: %w", err)
		}
		// Re-detect after install in case Redis was just installed but already configured elsewhere.
		if !redisReused {
			if ep, ok := detectExistingRedis(); ok {
				applyExistingRedis(cfg, ep)
				existingRedis = ep
				redisReused = true
				fmt.Printf("==> Redis became available at %s:%d — reusing it\n", ep.Host, ep.Port)
			}
		}
	} else {
		fmt.Println("==> Skipping package install (--skip-install); verifying binaries...")
		need := []string{"psql", "incus"}
		if !redisReused {
			need = append(need, "redis-server")
		}
		for _, bin := range need {
			if _, err := exec.LookPath(bin); err != nil {
				return nil, fmt.Errorf("%s not found in PATH; install it or omit --skip-install", bin)
			}
		}
	}

	fmt.Println("==> Configuring PostgreSQL on 127.0.0.1:9601...")
	if err := configurePostgreSQL(cfg); err != nil {
		return nil, fmt.Errorf("configure postgresql: %w", err)
	}

	if redisReused {
		fmt.Printf("==> Keeping existing Redis at %s (no port/password changes)\n", formatRedisAddr(cfg))
		if existingRedis != nil && existingRedis.Source == "running-needs-auth" && cfg.Redis.Password == "" {
			fmt.Println("    Warning: Redis requires AUTH but requirepass was not found in config; set redis.password in the config manually")
		}
	} else {
		fmt.Println("==> Configuring new Redis on 127.0.0.1:9602...")
		if err := configureRedis(cfg); err != nil {
			return nil, fmt.Errorf("configure redis: %w", err)
		}
	}

	fmt.Println("==> Initializing Incus...")
	pool, err := configureIncus()
	if err != nil {
		return nil, fmt.Errorf("configure incus: %w", err)
	}
	if pool != "" {
		cfg.Incus.StoragePool = pool
	}
	_ = incusclient.EnsureHostIsolation()

	fmt.Println("==> Writing config and installing service files...")
	if err := config.Save(cfgPath, cfg); err != nil {
		return nil, err
	}
	if err := installSupportFiles(); err != nil {
		return nil, err
	}

	return &Result{
		ConfigPath:  cfgPath,
		APIKeys:     append([]string(nil), cfg.Auth.APIKeys...),
		DBPassword:  cfg.Database.Password,
		RedisPass:   cfg.Redis.Password,
		RedisHost:   cfg.Redis.Host,
		RedisPort:   cfg.Redis.Port,
		RedisReused: redisReused,
	}, nil
}

func installDependencies(installRedis bool) error {
	pm := detectPackageManager()
	switch pm {
	case "apt":
		if err := runEnv(map[string]string{"DEBIAN_FRONTEND": "noninteractive"}, "apt-get", "update", "-y"); err != nil {
			return err
		}
		pkgs := []string{"postgresql", "postgresql-contrib", "curl", "gnupg", "ca-certificates", "lvm2", "thin-provisioning-tools"}
		if installRedis {
			pkgs = append(pkgs, "redis-server")
		} else {
			fmt.Println("    Skipping Redis package install (existing Redis will be used)")
		}
		args := append([]string{"install", "-y"}, pkgs...)
		if err := runEnv(map[string]string{"DEBIAN_FRONTEND": "noninteractive"}, "apt-get", args...); err != nil {
			return err
		}
		return installIncusZabbly()
	case "dnf":
		pkgs := []string{"postgresql-server", "postgresql", "curl", "gnupg2", "lvm2"}
		if installRedis {
			pkgs = append(pkgs, "redis")
		} else {
			fmt.Println("    Skipping Redis package install (existing Redis will be used)")
		}
		args := append([]string{"install", "-y"}, pkgs...)
		if err := run("dnf", args...); err != nil {
			return err
		}
		_ = run("postgresql-setup", "--initdb")
		return installIncusZabbly()
	default:
		return fmt.Errorf("unsupported package manager; install postgresql, redis-server, and incus, then re-run: goincus init --skip-install")
	}
}

func installIncusZabbly() error {
	if _, err := exec.LookPath("incus"); err == nil {
		fmt.Println("    Incus already installed")
		return nil
	}

	script := `
set -euo pipefail
mkdir -p /etc/apt/keyrings
curl -fsSL https://pkgs.zabbly.com/key.asc | gpg --dearmor -o /etc/apt/keyrings/zabbly.gpg
. /etc/os-release
arch="$(dpkg --print-architecture)"
cat >/etc/apt/sources.list.d/zabbly-incus-stable.sources <<EOF
Enabled: yes
Types: deb
URIs: https://pkgs.zabbly.com/incus/stable
Suites: ${VERSION_CODENAME}
Components: main
Architectures: ${arch}
Signed-By: /etc/apt/keyrings/zabbly.gpg
EOF
apt-get update -y
DEBIAN_FRONTEND=noninteractive apt-get install -y incus
`
	if detectPackageManager() == "apt" {
		if err := run("bash", "-c", script); err != nil {
			return fmt.Errorf("install incus (zabbly): %w", err)
		}
		return nil
	}

	if err := run("bash", "-c", "curl -fsSL https://pkgs.zabbly.com/get/incus-stable | bash"); err != nil {
		return fmt.Errorf("install incus: %w (see https://linuxcontainers.org/incus/docs/main/installing/)", err)
	}
	if _, err := exec.LookPath("incus"); err != nil {
		return fmt.Errorf("incus binary missing after install")
	}
	return nil
}

func configurePostgreSQL(cfg *config.Config) error {
	port := cfg.Database.Port
	user := cfg.Database.User
	pass := cfg.Database.Password
	dbname := cfg.Database.Name

	confDir := findPostgresConfDir()
	if confDir == "" {
		return fmt.Errorf("could not locate postgresql.conf directory")
	}
	dropIn := filepath.Join(confDir, "conf.d")
	_ = os.MkdirAll(dropIn, 0o755)
	content := fmt.Sprintf(`# Managed by goincus init
listen_addresses = '127.0.0.1'
port = %d
password_encryption = scram-sha-256
`, port)
	if err := os.WriteFile(filepath.Join(dropIn, "goincus.conf"), []byte(content), 0o644); err != nil {
		return err
	}

	hba := filepath.Join(confDir, "pg_hba.conf")
	if data, err := os.ReadFile(hba); err == nil {
		entry := "host    goincus         goincus         127.0.0.1/32            scram-sha-256\n"
		if !strings.Contains(string(data), "goincus         goincus") {
			_ = os.WriteFile(hba, append(data, []byte(entry)...), 0o640)
		}
	}

	_ = run("systemctl", "enable", "--now", "postgresql")
	_ = run("systemctl", "restart", "postgresql")
	time.Sleep(2 * time.Second)

	createRole := fmt.Sprintf(`DO $$ BEGIN
  CREATE ROLE %s LOGIN PASSWORD '%s';
EXCEPTION WHEN duplicate_object THEN
  ALTER ROLE %s WITH PASSWORD '%s';
END $$;`, user, escapeSQL(pass), user, escapeSQL(pass))
	if err := runAsPostgres("psql", "-v", "ON_ERROR_STOP=1", "-c", createRole); err != nil {
		return fmt.Errorf("create role: %w", err)
	}

	out, _ := outputAsPostgres("psql", "-tAc", fmt.Sprintf(`SELECT 1 FROM pg_database WHERE datname='%s'`, dbname))
	if strings.TrimSpace(out) != "1" {
		if err := runAsPostgres("psql", "-v", "ON_ERROR_STOP=1", "-c",
			fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, dbname, user)); err != nil {
			return fmt.Errorf("create database: %w", err)
		}
	}
	_ = runAsPostgres("psql", "-c", fmt.Sprintf(`GRANT ALL PRIVILEGES ON DATABASE %s TO %s`, dbname, user))
	return nil
}

func configureRedis(cfg *config.Config) error {
	conf := fmt.Sprintf(`# Managed by goincus init
bind 127.0.0.1 -::1
port %d
protected-mode yes
requirepass %s
supervised systemd
daemonize no
`, cfg.Redis.Port, cfg.Redis.Password)

	if err := os.MkdirAll("/etc/redis", 0o755); err != nil {
		return err
	}
	dropPath := "/etc/redis/goincus.conf"
	if err := os.WriteFile(dropPath, []byte(conf), 0o640); err != nil {
		return err
	}

	for _, p := range []string{"/etc/redis/redis.conf", "/etc/redis.conf"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if !strings.Contains(string(data), dropPath) {
			_ = os.WriteFile(p, append(data, []byte("\ninclude "+dropPath+"\n")...), 0o644)
		}
		break
	}

	_ = run("systemctl", "enable", "--now", "redis-server")
	_ = run("systemctl", "enable", "--now", "redis")
	_ = run("systemctl", "restart", "redis-server")
	_ = run("systemctl", "restart", "redis")
	time.Sleep(1 * time.Second)
	return nil
}

func configureIncus() (string, error) {
	_ = run("systemctl", "enable", "--now", "incus")
	_ = run("systemctl", "enable", "--now", "incus.service")

	if err := run("incus", "info"); err != nil {
		preseed := `config: {}
networks:
- config:
    ipv4.address: auto
    ipv4.nat: "true"
    ipv6.address: none
  description: ""
  name: incusbr0
  type: bridge
storage_pools:
- config: {}
  description: ""
  name: default
  driver: dir
profiles:
- config: {}
  description: ""
  devices:
    eth0:
      name: eth0
      network: incusbr0
      type: nic
    root:
      path: /
      pool: default
      type: disk
  name: default
projects: []
cluster: null
`
		cmd := exec.Command("incus", "admin", "init", "--preseed")
		cmd.Stdin = strings.NewReader(preseed)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			cmd = exec.Command("incus", "init", "--preseed")
			cmd.Stdin = strings.NewReader(preseed)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return "", fmt.Errorf("incus init: %w", err)
			}
		}
	}

	// Already-initialized hosts may still lack the default bridge/pool.
	pool, err := ensureIncusNetworkAndPool()
	if err != nil {
		return "", err
	}
	if err := ensureHostIDMaps(); err != nil {
		return pool, err
	}
	return pool, nil
}

func ensureHostIDMaps() error {
	const entry = "root:1000000:1000000000"
	for _, path := range []string{"/etc/subuid", "/etc/subgid"} {
		data, err := os.ReadFile(path)
		if err != nil {
			if writeErr := os.WriteFile(path, []byte(entry+"\n"), 0o644); writeErr != nil {
				return fmt.Errorf("write %s: %w", path, writeErr)
			}
			continue
		}
		if strings.Contains(string(data), "root:") {
			continue
		}
		if !strings.HasSuffix(string(data), "\n") && len(data) > 0 {
			data = append(data, '\n')
		}
		if err := os.WriteFile(path, append(data, []byte(entry+"\n")...), 0o644); err != nil {
			return fmt.Errorf("update %s: %w", path, err)
		}
	}
	fmt.Println("    Ensured root entries in /etc/subuid and /etc/subgid")
	return nil
}

func ensureIncusNetworkAndPool() (string, error) {
	pool, err := ensureDFIsolatingStoragePool()
	if err != nil {
		return "", err
	}

	if err := run("incus", "network", "show", "incusbr0"); err != nil {
		// Reuse any existing managed bridge if present.
		if out, err := exec.Command("incus", "network", "list", "-f", "csv", "-c", "n,t").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				parts := strings.Split(strings.TrimSpace(line), ",")
				if len(parts) >= 2 && parts[1] == "bridge" && parts[0] != "" {
					fmt.Printf("    Using existing Incus network %q\n", parts[0])
					_ = run("incus", "profile", "device", "set", "default", "eth0", "network="+parts[0])
					_ = run("incus", "profile", "device", "add", "default", "root", "disk", "path=/", "pool="+pool)
					return pool, nil
				}
			}
		}
		if err := run("incus", "network", "create", "incusbr0", "ipv4.address=auto", "ipv4.nat=true", "ipv6.address=none"); err != nil {
			return "", fmt.Errorf("create network incusbr0: %w", err)
		}
	}

	_ = run("incus", "profile", "device", "add", "default", "eth0", "nic", "network=incusbr0", "name=eth0")
	_ = run("incus", "profile", "device", "set", "default", "eth0", "network=incusbr0")
	_ = run("incus", "profile", "device", "add", "default", "root", "disk", "path=/", "pool="+pool)
	_ = run("incus", "profile", "device", "set", "default", "root", "pool="+pool)
	return pool, nil
}

// ensureDFIsolatingStoragePool creates/selects zfs or lvm so guest `df` shows storage_gb.
// dir/btrfs always report the host/pool size in df — never use them for goincus VPS roots.
func ensureDFIsolatingStoragePool() (string, error) {
	candidates := []string{"goincus", "goincus-lvm", "goincus-zfs"}
	for _, name := range candidates {
		driver, err := incusStorageDriver(name)
		if err == nil && isDFIsolatingStorageDriver(driver) {
			fmt.Printf("    Using Incus storage pool %s (%s)\n", name, driver)
			return name, nil
		}
	}

	for _, name := range candidates {
		if _, err := incusStorageDriver(name); err == nil {
			continue // exists but wrong driver
		}
		for _, driver := range []string{"zfs", "lvm"} {
			args := []string{"storage", "create", name, driver, "size=200GiB"}
			if err := run("incus", args...); err == nil {
				fmt.Printf("    Created Incus storage pool %s (%s) — guest df will show disk quota\n", name, driver)
				return name, nil
			}
		}
	}

	return "", fmt.Errorf(
		"need zfs or lvm storage for disk isolation (dir/btrfs show host disk in guest df). "+
			"Install: apt install -y lvm2 thin-provisioning-tools   # or: apt install -y zfsutils-linux\n"+
			"Then: incus storage create goincus-lvm lvm size=200GiB",
	)
}

func isDFIsolatingStorageDriver(driver string) bool {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "zfs", "lvm", "lvmcluster", "ceph":
		return true
	default:
		return false
	}
}

func incusStorageDriver(name string) (string, error) {
	out, err := exec.Command("incus", "storage", "show", name).Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "driver:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "driver:")), nil
		}
	}
	return "", fmt.Errorf("driver not found for pool %s", name)
}

func installSupportFiles() error {
	if err := os.MkdirAll(MigrationsDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll("/var/log/goincus", 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll("/opt/goincus", 0o755); err != nil {
		return err
	}

	if self, err := os.Executable(); err == nil {
		_ = run("install", "-m", "0755", self, DefaultBinaryPath)
	}

	unit := `[Unit]
Description=goincus — Incus NAT VPS provisioning API
Documentation=https://github.com/hdmain/goincus
After=network-online.target postgresql.service redis-server.service redis.service incus.service goincus-net.service
Wants=network-online.target goincus-net.service
Requires=incus.service

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=/opt/goincus
ExecStart=/usr/local/bin/goincus serve -config /etc/goincus/config.yaml -migrations /usr/share/goincus/migrations
Restart=on-failure
RestartSec=5s
TimeoutStopSec=30s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelModules=true
ProtectControlGroups=true
LockPersonality=true
RestrictSUIDSGID=true
RestrictRealtime=true
ReadWritePaths=/var/lib/incus /run/incus /var/log/goincus
ReadOnlyPaths=/etc/goincus
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
StandardOutput=journal
StandardError=journal
SyslogIdentifier=goincus

[Install]
WantedBy=multi-user.target
`
	if err := os.WriteFile(DefaultUnitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	_ = os.MkdirAll("/usr/local/libexec/goincus", 0o755)
	natScript := `#!/bin/sh
set -eu
BR="${GOINCUS_BRIDGE:-incusbr0}"
SUBNET="${GOINCUS_SUBNET:-10.72.160.0/24}"
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
`
	if err := os.WriteFile("/usr/local/libexec/goincus/ensure-host-nat.sh", []byte(natScript), 0o755); err != nil {
		return err
	}
	storageScript := `#!/bin/sh
set -eu
POOL_LVM="${GOINCUS_STORAGE_POOL:-goincus-lvm}"
POOL_ZFS="${GOINCUS_STORAGE_POOL_ZFS:-goincus-zfs}"
SIZE="${GOINCUS_STORAGE_SIZE:-200GiB}"
log() { echo "goincus-storage: $*"; }
have_bin() { command -v "$1" >/dev/null 2>&1; }
install_pkgs() {
  if have_bin lvcreate && have_bin vgcreate; then return 0; fi
  [ "$(id -u)" -eq 0 ] || return 1
  export DEBIAN_FRONTEND=noninteractive
  if have_bin apt-get; then
    log "installing lvm2 thin-provisioning-tools"
    apt-get update -y >/dev/null 2>&1 || true
    apt-get install -y lvm2 thin-provisioning-tools
    apt-get install -y zfsutils-linux >/dev/null 2>&1 || true
  elif have_bin dnf; then dnf install -y lvm2
  elif have_bin yum; then yum install -y lvm2
  else return 1
  fi
}
pool_driver() { incus storage show "$1" 2>/dev/null | awk -F': ' '/^driver:/{print $2; exit}'; }
is_good_driver() { case "$1" in zfs|lvm|lvmcluster|ceph) return 0 ;; *) return 1 ;; esac; }
ensure_pool() {
  name="$1"; driver="$2"
  cur="$(pool_driver "$name" || true)"
  if [ -n "$cur" ]; then
    is_good_driver "$cur" && { log "pool $name already ok ($cur)"; return 0; }
    return 1
  fi
  have_bin incus || return 1
  case "$driver" in lvm) have_bin lvcreate || return 1 ;; zfs) have_bin zpool || return 1 ;; esac
  log "creating pool $name ($driver size=$SIZE)"
  incus storage create "$name" "$driver" "size=$SIZE"
}
install_pkgs || true
for name in goincus "$POOL_LVM" "$POOL_ZFS"; do
  d="$(pool_driver "$name" || true)"
  is_good_driver "$d" && { log "ready: $name ($d)"; exit 0; }
done
ensure_pool "$POOL_LVM" lvm && exit 0
ensure_pool goincus lvm && exit 0
ensure_pool "$POOL_ZFS" zfs && exit 0
ensure_pool goincus zfs && exit 0
log "failed to create zfs/lvm pool"
exit 1
`
	if err := os.WriteFile("/usr/local/libexec/goincus/ensure-host-storage.sh", []byte(storageScript), 0o755); err != nil {
		return err
	}
	netUnit := `[Unit]
Description=goincus host NAT/FORWARD + storage pool bootstrap (Docker-safe)
After=network-online.target incus.service docker.service
Wants=network-online.target
Wants=incus.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/libexec/goincus/ensure-host-storage.sh
ExecStart=/usr/local/libexec/goincus/ensure-host-nat.sh

[Install]
WantedBy=multi-user.target
`
	if err := os.WriteFile("/etc/systemd/system/goincus-net.service", []byte(netUnit), 0o644); err != nil {
		return err
	}
	_ = run("systemctl", "daemon-reload")
	_ = run("systemctl", "enable", "--now", "goincus-net")
	return WriteEmbeddedMigrations(MigrationsDir)
}

func detectPackageManager() string {
	if _, err := exec.LookPath("apt-get"); err == nil {
		return "apt"
	}
	if _, err := exec.LookPath("dnf"); err == nil {
		return "dnf"
	}
	return ""
}

func findPostgresConfDir() string {
	base := "/etc/postgresql"
	entries, err := os.ReadDir(base)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			main := filepath.Join(base, e.Name(), "main")
			if _, err := os.Stat(filepath.Join(main, "postgresql.conf")); err == nil {
				return main
			}
		}
	}
	for _, p := range []string{"/var/lib/pgsql/data", "/var/lib/postgresql/data"} {
		if _, err := os.Stat(filepath.Join(p, "postgresql.conf")); err == nil {
			return p
		}
	}
	return ""
}

func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runEnv(env map[string]string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runAsPostgres(name string, args ...string) error {
	cmd := exec.Command("runuser", "-u", "postgres", "--", name)
	cmd.Args = append(cmd.Args, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err == nil {
		return nil
	}
	quoted := shellQuoteAll(args)
	cmd = exec.Command("su", "-", "postgres", "-c", name+" "+strings.Join(quoted, " "))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func outputAsPostgres(name string, args ...string) (string, error) {
	cmd := exec.Command("runuser", "-u", "postgres", "--", name)
	cmd.Args = append(cmd.Args, args...)
	out, err := cmd.Output()
	if err == nil {
		return string(out), nil
	}
	quoted := shellQuoteAll(args)
	cmd = exec.Command("su", "-", "postgres", "-c", name+" "+strings.Join(quoted, " "))
	out, err = cmd.Output()
	return string(out), err
}

func shellQuoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return out
}
