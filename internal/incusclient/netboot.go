package incusclient

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	incus "github.com/lxc/incus/v6/client"
)

const hostDebCacheRoot = "/var/cache/goincus/debs"

type guestOS struct {
	ID       string // ubuntu, debian
	Codename string // noble, bookworm, trixie, ...
	Arch     string // amd64, arm64
}

func (g guestOS) cacheDir() string {
	id := g.ID
	if id == "" {
		id = "unknown"
	}
	code := g.Codename
	if code == "" {
		code = "unknown"
	}
	arch := g.Arch
	if arch == "" {
		arch = "amd64"
	}
	return filepath.Join(hostDebCacheRoot, id+"-"+code+"-"+arch)
}

// HardenNetwork ensures NAT + DNS are enabled on a managed bridge.
func (c *Client) HardenNetwork(name string) error {
	n, etag, err := c.server.GetNetwork(name)
	if err != nil {
		return err
	}
	if n.Config == nil {
		n.Config = map[string]string{}
	}
	changed := false
	set := func(k, v string) {
		if n.Config[k] != v {
			n.Config[k] = v
			changed = true
		}
	}
	if n.Config["ipv4.address"] == "" || strings.EqualFold(n.Config["ipv4.address"], "none") {
		set("ipv4.address", "auto")
	}
	set("ipv4.nat", "true")
	set("ipv4.dhcp", "true")
	if n.Config["dns.mode"] == "" {
		set("dns.mode", "managed")
	}
	set("ipv6.address", "none")
	if !changed {
		return nil
	}
	return c.server.UpdateNetwork(name, n.Writable(), etag)
}

// ConfigureGuestDNS brings up the NIC if needed and writes public resolvers.
func (c *Client) ConfigureGuestDNS(name string) error {
	bridgeDNS := ""
	if netName := c.cfg.Network; netName != "" {
		if n, _, err := c.server.GetNetwork(netName); err == nil {
			if cidr := n.Config["ipv4.address"]; cidr != "" && !strings.EqualFold(cidr, "none") {
				if host, _, err := parseHostFromCIDR(cidr); err == nil {
					bridgeDNS = host
				}
			}
		}
	}
	script := `set -eux
for iface in eth0 enp0s3; do
  ip link set "$iface" up 2>/dev/null || true
done
for i in $(seq 1 15); do
  if ip -4 -o addr show scope global 2>/dev/null | grep -q .; then
    break
  fi
  command -v dhclient >/dev/null 2>&1 && dhclient -1 eth0 2>/dev/null || true
  sleep 1
done
umount /etc/resolv.conf 2>/dev/null || true
rm -f /etc/resolv.conf
cat > /etc/resolv.conf <<'EOF'
nameserver 1.1.1.1
nameserver 8.8.8.8
EOF
`
	if bridgeDNS != "" {
		script += fmt.Sprintf("echo 'nameserver %s' >> /etc/resolv.conf\n", bridgeDNS)
	}
	script += `
mkdir -p /etc
touch /etc/gai.conf
grep -q '^precedence ::ffff:0:0/96  100' /etc/gai.conf 2>/dev/null || echo 'precedence ::ffff:0:0/96  100' >> /etc/gai.conf
`
	_, stderr, code, err := c.exec(name, script)
	if err != nil {
		return fmt.Errorf("configure dns: %w (%s)", err, stderr)
	}
	if code != 0 {
		return fmt.Errorf("configure dns exited %d: %s", code, stderr)
	}
	return nil
}

func parseHostFromCIDR(cidr string) (string, string, error) {
	parts := strings.Split(cidr, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", fmt.Errorf("bad cidr %q", cidr)
	}
	mask := "24"
	if len(parts) > 1 {
		mask = parts[1]
	}
	return parts[0], mask, nil
}

func (c *Client) detectGuestOS(name string) (guestOS, error) {
	stdout, stderr, code, err := c.exec(name, `
set -e
. /etc/os-release
echo "ID=${ID:-}"
echo "VERSION_CODENAME=${VERSION_CODENAME:-}"
echo "ARCH=$(dpkg --print-architecture 2>/dev/null || uname -m)"
`)
	if err != nil {
		return guestOS{}, fmt.Errorf("detect guest os: %w (%s)", err, stderr)
	}
	if code != 0 {
		return guestOS{}, fmt.Errorf("detect guest os exited %d (%s)", code, stderr)
	}
	g := guestOS{Arch: "amd64"}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "ID="):
			g.ID = strings.Trim(strings.TrimPrefix(line, "ID="), `"`)
		case strings.HasPrefix(line, "VERSION_CODENAME="):
			g.Codename = strings.Trim(strings.TrimPrefix(line, "VERSION_CODENAME="), `"`)
		case strings.HasPrefix(line, "ARCH="):
			arch := strings.TrimPrefix(line, "ARCH=")
			switch arch {
			case "x86_64":
				g.Arch = "amd64"
			case "aarch64":
				g.Arch = "arm64"
			default:
				if arch != "" {
					g.Arch = arch
				}
			}
		}
	}
	if g.ID == "" || g.Codename == "" {
		return guestOS{}, fmt.Errorf("incomplete guest os-release (id=%q codename=%q)", g.ID, g.Codename)
	}
	return g, nil
}

func guestAptSources(g guestOS) (string, error) {
	switch g.ID {
	case "ubuntu":
		mirror := "http://archive.ubuntu.com/ubuntu"
		if g.Arch == "arm64" || g.Arch == "armhf" {
			mirror = "http://ports.ubuntu.com/ubuntu-ports"
		}
		return fmt.Sprintf(`deb [trusted=yes] %s %s main universe
deb [trusted=yes] %s %s-updates main universe
deb [trusted=yes] %s %s-security main universe
`, mirror, g.Codename, mirror, g.Codename, strings.Replace(mirror, "archive.ubuntu.com/ubuntu", "security.ubuntu.com/ubuntu", 1), g.Codename), nil
	case "debian":
		return fmt.Sprintf(`deb [trusted=yes] http://deb.debian.org/debian %s main
deb [trusted=yes] http://deb.debian.org/debian %s-updates main
deb [trusted=yes] http://security.debian.org/debian-security %s-security main
`, g.Codename, g.Codename, g.Codename), nil
	default:
		return "", fmt.Errorf("unsupported guest os %q for offline openssh install", g.ID)
	}
}

// EnsureOpenSSHDebs downloads OpenSSH (+ missing hard deps) for the guest distro
// using an isolated apt against that distro's mirrors (host OS may differ).
func EnsureOpenSSHDebs(g guestOS) error {
	dir := g.cacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "openssh-server_*.deb")); len(matches) > 0 {
		return nil
	}

	sources, err := guestAptSources(g)
	if err != nil {
		return err
	}

	// Seed package set: Ubuntu base images already have libc/pam/etc.
	// Debian trixie openssh also needs runit-helper + libwtmpdb0.
	seed := "openssh-server openssh-client openssh-sftp-server libwrap0"
	if g.ID == "debian" {
		seed += " runit-helper libwtmpdb0"
	}

	script := fmt.Sprintf(`
set -euo pipefail
CACHE=%q
ARCH=%q
SEED=%q
export DEBIAN_FRONTEND=noninteractive
TMP="$(mktemp -d)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT
mkdir -p "$TMP/var/lib/apt/lists/partial" "$CACHE/partial"
cat > "$TMP/sources.list" <<'EOS'
%s
EOS
rm -f "$CACHE"/*.deb
APT=(apt-get
  -o Dir::Etc::sourcelist="$TMP/sources.list"
  -o Dir::Etc::sourceparts=/dev/null
  -o Dir::State::Lists="$TMP/var/lib/apt/lists"
  -o Dir::Cache::Archives="$CACHE"
  -o APT::Architecture="$ARCH"
  -o APT::Get::AllowUnauthenticated=true
  -o Acquire::AllowInsecureRepositories=true
  -o Acquire::AllowDowngradeToInsecureRepositories=true
)
"${APT[@]}" update -qq
cd "$CACHE"
"${APT[@]}" download $SEED
# Pull a few rounds of non-base Depends declared by the downloaded debs.
for _ in 1 2 3 4; do
  need=""
  for deb in *.deb; do
    [ -f "$deb" ] || continue
    deps="$(dpkg-deb -f "$deb" Depends Pre-Depends 2>/dev/null || true)"
    deps="$(printf '%%s\n' "$deps" | tr ',' '\n' | sed -E 's/\([^)]*\)//g; s/\|.*//; s/^[[:space:]]+//; s/[[:space:]]+$//' | grep -E '^[a-z0-9][a-z0-9+.-]*$' || true)"
    for p in $deps; do
      case "$p" in
        libc6|libgcc-s1|base-files|bash|coreutils|debianutils|sed|grep|awk|perl|perl-base|init-system-helpers|libpam0g|libpam-modules|libpam-runtime|libselinux1|libaudit1|libcrypt1|zlib1g|adduser|passwd|login|util-linux|libsystemd0|libudev1|ucf|debconf|lsb-base|sysvinit-utils|libgssapi-krb5-2|libkrb5-3|libkeyutils1|libnsl2|libtirpc3|libbsd0|libedit2|libmd0|libssl3|libssl3t64|libc6-*) continue ;;
      esac
      ls "${p}_"*.deb >/dev/null 2>&1 && continue
      need="$need $p"
    done
  done
  need="$(printf '%%s\n' $need | tr ' ' '\n' | sort -u | tr '\n' ' ')"
  [ -n "$(echo "$need" | tr -d '[:space:]')" ] || break
  "${APT[@]}" download $need || true
done
ls "$CACHE"/openssh-server_*.deb >/dev/null
`, dir, g.Arch, seed, sources)

	cmd := exec.Command("bash", "-lc", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("download openssh debs for %s/%s/%s: %w (%s)", g.ID, g.Codename, g.Arch, err, string(out))
	}
	return nil
}

// InstallOpenSSHFromHost pushes distro-matched .debs into the guest and installs with dpkg only.
func (c *Client) InstallOpenSSHFromHost(name string) error {
	g, err := c.detectGuestOS(name)
	if err != nil {
		return err
	}
	if err := EnsureOpenSSHDebs(g); err != nil {
		return err
	}
	dir := g.cacheDir()
	debs, err := filepath.Glob(filepath.Join(dir, "*.deb"))
	if err != nil || len(debs) == 0 {
		return fmt.Errorf("no debs in %s", dir)
	}
	_, _, _, _ = c.exec(name, "mkdir -p /var/tmp/goincus-debs && rm -rf /var/tmp/goincus-debs/*")
	for _, deb := range debs {
		base := filepath.Base(deb)
		f, err := os.Open(deb)
		if err != nil {
			return err
		}
		dest := "/var/tmp/goincus-debs/" + base
		err = c.server.CreateInstanceFile(name, dest, incus.InstanceFileArgs{
			Content: f,
			Mode:    0o644,
			Type:    "file",
		})
		_ = f.Close()
		if err != nil {
			return fmt.Errorf("push %s: %w", deb, err)
		}
	}
	// Never apt-get -f here: without guest DNS it removes the half-installed server package.
	stdout, stderr, code, err := c.exec(name, `
set -eux
export DEBIAN_FRONTEND=noninteractive
dpkg -i /var/tmp/goincus-debs/*.deb
test -x /usr/sbin/sshd || command -v sshd >/dev/null
`)
	if err != nil {
		return fmt.Errorf("dpkg install: %w (%s %s)", err, stdout, stderr)
	}
	if code != 0 {
		return fmt.Errorf("dpkg install exited %d (%s %s)", code, stdout, stderr)
	}
	return nil
}

// EnsureHostOpenSSHDebs is a best-effort warm cache for the default Ubuntu LTS guest.
func EnsureHostOpenSSHDebs() error {
	return EnsureOpenSSHDebs(guestOS{ID: "ubuntu", Codename: "noble", Arch: "amd64"})
}
