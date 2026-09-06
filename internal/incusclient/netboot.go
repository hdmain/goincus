package incusclient

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	incus "github.com/lxc/incus/v6/client"
)

const hostDebCache = "/var/cache/goincus/debs"

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

// ConfigureGuestDNS writes public resolvers so apt/ssh bootstrap can resolve names
// even when systemd-resolved/DHCP DNS is broken inside the guest.
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
# Prefer IPv4 for apt mirrors.
mkdir -p /etc/gai.conf
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

// EnsureHostOpenSSHDebs downloads OpenSSH packages on the host for offline guest install.
func EnsureHostOpenSSHDebs() error {
	if err := os.MkdirAll(hostDebCache, 0o755); err != nil {
		return err
	}
	matches, _ := filepath.Glob(filepath.Join(hostDebCache, "openssh-server_*.deb"))
	if len(matches) > 0 {
		return nil
	}
	script := fmt.Sprintf(`
set -e
cd %q
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y --download-only openssh-server
# Prefer freshly downloaded archives for this install.
cp -n /var/cache/apt/archives/openssh-*.deb . 2>/dev/null || true
cp -n /var/cache/apt/archives/libwrap0*.deb . 2>/dev/null || true
apt-get download openssh-server openssh-client openssh-sftp-server libwrap0 2>/dev/null || true
ls openssh-server_*.deb >/dev/null
`, hostDebCache)
	cmd := exec.Command("bash", "-lc", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("download openssh debs on host: %w (%s)", err, string(out))
	}
	return nil
}

// InstallOpenSSHFromHost pushes host-cached .debs into the guest and installs them with dpkg.
func (c *Client) InstallOpenSSHFromHost(name string) error {
	if err := EnsureHostOpenSSHDebs(); err != nil {
		return err
	}
	debs, err := filepath.Glob(filepath.Join(hostDebCache, "*.deb"))
	if err != nil || len(debs) == 0 {
		return fmt.Errorf("no debs in %s", hostDebCache)
	}
	_, _, _, _ = c.exec(name, "mkdir -p /var/tmp/goincus-debs && rm -f /var/tmp/goincus-debs/*.deb")
	for _, deb := range debs {
		f, err := os.Open(deb)
		if err != nil {
			return err
		}
		dest := "/var/tmp/goincus-debs/" + filepath.Base(deb)
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
	stdout, stderr, code, err := c.exec(name, `
set -eux
export DEBIAN_FRONTEND=noninteractive
dpkg -i /var/tmp/goincus-debs/*.deb || apt-get -f install -y || true
dpkg -i /var/tmp/goincus-debs/*.deb
command -v sshd >/dev/null || command -v /usr/sbin/sshd >/dev/null
`)
	if err != nil {
		return fmt.Errorf("dpkg install: %w (%s %s)", err, stdout, stderr)
	}
	if code != 0 {
		return fmt.Errorf("dpkg install exited %d (%s %s)", code, stdout, stderr)
	}
	return nil
}
