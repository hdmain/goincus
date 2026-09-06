package incusclient

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

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

type debPkg struct {
	Name     string
	Version  string
	Filename string
	Depends  string
	PreDep   string
	Mirror   string // archive root the Packages index came from
}

func guestMirrorRoots(g guestOS) ([]string, error) {
	switch g.ID {
	case "ubuntu":
		if g.Arch == "arm64" || g.Arch == "armhf" {
			return []string{"http://ports.ubuntu.com/ubuntu-ports"}, nil
		}
		return []string{
			"http://archive.ubuntu.com/ubuntu",
			"http://security.ubuntu.com/ubuntu",
		}, nil
	case "debian":
		return []string{
			"http://deb.debian.org/debian",
			"http://security.debian.org/debian-security",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported guest os %q for offline openssh install", g.ID)
	}
}

func guestIndexURLs(g guestOS) ([]string, error) {
	roots, err := guestMirrorRoots(g)
	if err != nil {
		return nil, err
	}
	var urls []string
	switch g.ID {
	case "ubuntu":
		suites := []string{g.Codename, g.Codename + "-updates", g.Codename + "-security"}
		comps := []string{"main", "universe"}
		for _, root := range roots {
			for _, suite := range suites {
				for _, comp := range comps {
					urls = append(urls, fmt.Sprintf("%s/dists/%s/%s/binary-%s/Packages.gz", root, suite, comp, g.Arch))
				}
			}
		}
	case "debian":
		// security uses a different suite naming (e.g. trixie-security).
		type sc struct{ root, suite, comp string }
		var specs []sc
		for _, root := range roots {
			if strings.Contains(root, "security") {
				specs = append(specs, sc{root, g.Codename + "-security", "main"})
				continue
			}
			for _, suite := range []string{g.Codename, g.Codename + "-updates"} {
				specs = append(specs, sc{root, suite, "main"})
			}
		}
		for _, s := range specs {
			urls = append(urls, fmt.Sprintf("%s/dists/%s/%s/binary-%s/Packages.gz", s.root, s.suite, s.comp, g.Arch))
		}
	}
	return urls, nil
}

func httpGet(url string) (io.ReadCloser, error) {
	client := &http.Client{Timeout: 90 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "goincus/openssh-bootstrap")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

func loadPackagesIndex(g guestOS) (map[string]debPkg, error) {
	urls, err := guestIndexURLs(g)
	if err != nil {
		return nil, err
	}
	out := map[string]debPkg{}
	var errs []string
	for _, u := range urls {
		body, err := httpGet(u)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		gz, err := gzip.NewReader(body)
		if err != nil {
			_ = body.Close()
			errs = append(errs, err.Error())
			continue
		}
		pkgs, err := parsePackages(gz)
		_ = gz.Close()
		_ = body.Close()
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		for name, pkg := range pkgs {
			pkg.Mirror = strings.Split(u, "/dists/")[0]
			prev, ok := out[name]
			if !ok || debVersionPrefer(pkg.Version, prev.Version) {
				out[name] = pkg
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no package indexes loaded for %s/%s: %s", g.ID, g.Codename, strings.Join(errs, "; "))
	}
	return out, nil
}

// debVersionPrefer is a coarse version pick: prefer strings that look newer by Debian epoch/upstream.
// Good enough for choosing updates over base when both appear.
func debVersionPrefer(a, b string) bool {
	if a == b {
		return false
	}
	// Prefer higher epoch.
	ae, ar := splitEpoch(a)
	be, br := splitEpoch(b)
	if ae != be {
		return ae > be
	}
	return ar > br // lexicographic fallback; updates usually sort higher
}

func splitEpoch(v string) (int, string) {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		var e int
		fmt.Sscanf(v[:i], "%d", &e)
		return e, v[i+1:]
	}
	return 0, v
}

func parsePackages(r io.Reader) (map[string]debPkg, error) {
	out := map[string]debPkg{}
	sc := bufio.NewScanner(r)
	// Some Depends lines are long.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var cur debPkg
	flush := func() {
		if cur.Name == "" || cur.Filename == "" {
			cur = debPkg{}
			return
		}
		out[cur.Name] = cur
		cur = debPkg{}
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "Package":
			if cur.Name != "" {
				flush()
			}
			cur.Name = val
		case "Version":
			cur.Version = val
		case "Filename":
			cur.Filename = val
		case "Depends":
			cur.Depends = val
		case "Pre-Depends":
			cur.PreDep = val
		}
	}
	flush()
	return out, sc.Err()
}

func parseDepNames(s string) []string {
	if s == "" {
		return nil
	}
	var names []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Keep first alternative only (foo | bar).
		if i := strings.Index(part, "|"); i >= 0 {
			part = part[:i]
		}
		part = strings.TrimSpace(part)
		name := strings.Fields(part)[0]
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

var baseDebSkip = map[string]struct{}{
	"libc6": {}, "libgcc-s1": {}, "base-files": {}, "bash": {}, "coreutils": {},
	"debianutils": {}, "sed": {}, "grep": {}, "awk": {}, "perl": {}, "perl-base": {},
	"init-system-helpers": {}, "libpam0g": {}, "libpam-modules": {}, "libpam-runtime": {},
	"libselinux1": {}, "libaudit1": {}, "libcrypt1": {}, "zlib1g": {}, "adduser": {},
	"passwd": {}, "login": {}, "util-linux": {}, "libsystemd0": {}, "libudev1": {},
	"ucf": {}, "debconf": {}, "lsb-base": {}, "sysvinit-utils": {},
	"libgssapi-krb5-2": {}, "libkrb5-3": {}, "libkeyutils1": {}, "libnsl2": {},
	"libtirpc3": {}, "libbsd0": {}, "libedit2": {}, "libmd0": {}, "libssl3": {},
	"libssl3t64": {}, "libcap-ng0": {}, "libkrb5support0": {}, "libk5crypto3": {},
}

func resolveOpenSSHDebs(index map[string]debPkg) ([]debPkg, error) {
	need := []string{"openssh-server", "openssh-client", "openssh-sftp-server", "libwrap0"}
	seen := map[string]struct{}{}
	var out []debPkg
	for len(need) > 0 {
		name := need[0]
		need = need[1:]
		if _, ok := seen[name]; ok {
			continue
		}
		if _, skip := baseDebSkip[name]; skip {
			continue
		}
		if strings.HasPrefix(name, "libc6") {
			continue
		}
		pkg, ok := index[name]
		if !ok {
			// Optional hard deps may be missing on some suites; require openssh-server.
			if name == "openssh-server" || name == "openssh-client" || name == "openssh-sftp-server" {
				return nil, fmt.Errorf("package %q not found in guest indexes", name)
			}
			continue
		}
		seen[name] = struct{}{}
		out = append(out, pkg)
		for _, dep := range parseDepNames(pkg.Depends + "," + pkg.PreDep) {
			if _, ok := seen[dep]; ok {
				continue
			}
			if _, skip := baseDebSkip[dep]; skip {
				continue
			}
			need = append(need, dep)
		}
	}
	return out, nil
}

func downloadDebFile(mirrorRoots []string, pkg debPkg, destDir string) error {
	base := path.Base(pkg.Filename)
	dest := filepath.Join(destDir, base)
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return nil
	}
	roots := make([]string, 0, len(mirrorRoots)+1)
	if pkg.Mirror != "" {
		roots = append(roots, pkg.Mirror)
	}
	roots = append(roots, mirrorRoots...)
	var lastErr error
	seen := map[string]struct{}{}
	for _, root := range roots {
		root = strings.TrimRight(root, "/")
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		url := root + "/" + strings.TrimLeft(pkg.Filename, "/")
		body, err := httpGet(url)
		if err != nil {
			lastErr = err
			continue
		}
		tmp := dest + ".partial"
		f, err := os.Create(tmp)
		if err != nil {
			_ = body.Close()
			return err
		}
		_, err = io.Copy(f, body)
		_ = body.Close()
		_ = f.Close()
		if err != nil {
			_ = os.Remove(tmp)
			lastErr = err
			continue
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	}
	return fmt.Errorf("download %s: %v", pkg.Filename, lastErr)
}

// EnsureOpenSSHDebs downloads OpenSSH (+ non-base hard deps) for the guest distro
// by parsing that distro's Packages.gz over HTTP (avoids host apt/keyring mismatch).
func EnsureOpenSSHDebs(g guestOS) error {
	dir := g.cacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "openssh-server_*.deb")); len(matches) > 0 {
		return nil
	}

	index, err := loadPackagesIndex(g)
	if err != nil {
		return err
	}
	pkgs, err := resolveOpenSSHDebs(index)
	if err != nil {
		return err
	}
	roots, err := guestMirrorRoots(g)
	if err != nil {
		return err
	}
	// Prefer the mirror that matches each package filename's pool host by trying all roots.
	for _, pkg := range pkgs {
		if err := downloadDebFile(roots, pkg, dir); err != nil {
			// Security packages live on security.debian.org / security.ubuntu.com;
			// Filename is relative to that suite's root. Try each root already.
			return fmt.Errorf("fetch %s: %w", pkg.Name, err)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "openssh-server_*.deb")); len(matches) == 0 {
		return fmt.Errorf("openssh-server deb missing after download in %s", dir)
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
