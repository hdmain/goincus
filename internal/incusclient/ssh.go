package incusclient

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// CloudInitUserData builds cloud-config that installs OpenSSH and sets the root password.
func CloudInitUserData(rootPassword string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(rootPassword))
	return fmt.Sprintf(`#cloud-config
package_update: true
packages:
  - openssh-server
ssh_pwauth: true
disable_root: false
runcmd:
  - [ bash, -lc, "PASS=$(echo '%s' | base64 -d); echo root:$PASS | chpasswd" ]
  - [ bash, -lc, "ssh-keygen -A" ]
  - [ bash, -lc, "mkdir -p /etc/ssh/sshd_config.d; printf '%%s\\n' 'PermitRootLogin yes' 'PasswordAuthentication yes' 'ListenAddress 0.0.0.0' 'ListenAddress 127.0.0.1' > /etc/ssh/sshd_config.d/99-goincus.conf" ]
  - [ bash, -lc, "systemctl disable --now ssh.socket 2>/dev/null || true; systemctl enable ssh 2>/dev/null || systemctl enable sshd 2>/dev/null || true; systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || true" ]
`, b64)
}

// EnsureSSH installs/configures OpenSSH inside a running container and sets the root password.
// It fails unless TCP/22 is confirmed listening afterward.
func (c *Client) EnsureSSH(name, rootPassword string) error {
	if rootPassword == "" {
		return fmt.Errorf("root password is empty")
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(rootPassword))
	script := fmt.Sprintf(`set -eux
export DEBIAN_FRONTEND=noninteractive
if command -v apt-get >/dev/null 2>&1; then
  apt-get update -y
  apt-get install -y openssh-server openssh-client iproute2
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y openssh-server iproute
elif command -v apk >/dev/null 2>&1; then
  apk add --no-cache openssh openssh-server iproute2
else
  echo "no supported package manager" >&2
  exit 1
fi

PASS="$(echo '%s' | base64 -d)"
echo "root:${PASS}" | chpasswd

ssh-keygen -A
mkdir -p /etc/ssh/sshd_config.d /run/sshd
cat > /etc/ssh/sshd_config.d/99-goincus.conf <<'EOF'
PermitRootLogin yes
PasswordAuthentication yes
KbdInteractiveAuthentication yes
UsePAM yes
AddressFamily inet
ListenAddress 0.0.0.0
ListenAddress 127.0.0.1
EOF

systemctl disable --now ssh.socket 2>/dev/null || true
systemctl disable --now sshd.socket 2>/dev/null || true
systemctl unmask ssh 2>/dev/null || true
systemctl unmask sshd 2>/dev/null || true
systemctl enable ssh 2>/dev/null || systemctl enable sshd 2>/dev/null || true
systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || service ssh restart || service sshd restart

sshd -t || /usr/sbin/sshd -t || true
for i in $(seq 1 30); do
  if ss -ltn | grep -E '[:.]22[[:space:]]' >/dev/null 2>&1; then
    ss -ltn | grep -E '[:.]22[[:space:]]' || true
    # Prove local accept works (proxy connects to 127.0.0.1).
    if command -v timeout >/dev/null 2>&1; then
      timeout 2 bash -c 'echo | openssl s_client -connect 127.0.0.1:22 2>/dev/null | head -1' >/dev/null 2>&1 || \
      timeout 2 bash -c 'exec 3<>/dev/tcp/127.0.0.1/22 && head -1 <&3' | grep -qi SSH || true
    fi
    exit 0
  fi
  if [ "$i" = "5" ] || [ "$i" = "15" ]; then
    /usr/sbin/sshd || true
  fi
  sleep 1
done
echo "sshd failed to listen on :22" >&2
ss -ltn || true
systemctl status ssh --no-pager || systemctl status sshd --no-pager || true
journalctl -u ssh -u sshd -n 50 --no-pager || true
exit 1
`, b64)

	stdout, stderr, code, err := c.exec(name, script)
	if err != nil {
		return fmt.Errorf("exec ensure ssh: %w; stderr=%s stdout=%s", err, stderr, stdout)
	}
	if code != 0 {
		return fmt.Errorf("ensure ssh exited %d; stderr=%s stdout=%s", code, stderr, stdout)
	}
	if err := c.WaitForSSHD(name, 90*time.Second); err != nil {
		return fmt.Errorf("%w; last ensure stdout=%s stderr=%s", err, stdout, stderr)
	}
	return nil
}

// WaitForSSHD polls until something listens on TCP/22 inside the container.
func (c *Client) WaitForSSHD(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stdout, _, code, err := c.exec(name, `ss -ltn | grep -E '[:.]22[[:space:]]'`)
		if err == nil && code == 0 && strings.Contains(stdout, ":22") {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("sshd did not become ready within %s", timeout)
}

func (c *Client) exec(name, script string) (stdout string, stderr string, exitCode int, err error) {
	var outBuf, errBuf bytes.Buffer
	req := api.InstanceExecPost{
		Command:     []string{"bash", "-lc", script},
		WaitForWS:   true,
		Interactive: false,
	}
	args := incus.InstanceExecArgs{
		Stdout: &outBuf,
		Stderr: &errBuf,
		Stdin:  strings.NewReader(""),
	}
	op, err := c.server.ExecInstance(name, req, &args)
	if err != nil {
		return outBuf.String(), errBuf.String(), -1, err
	}
	if err := op.Wait(); err != nil {
		return outBuf.String(), errBuf.String(), -1, err
	}

	exitCode = 0
	meta := op.Get().Metadata
	if meta != nil {
		switch v := meta["return"].(type) {
		case float64:
			exitCode = int(v)
		case int:
			exitCode = v
		}
	}
	return outBuf.String(), errBuf.String(), exitCode, nil
}

// AllocateContainerIPv4 picks a free address from the managed bridge pool for a static NIC.
func (c *Client) AllocateContainerIPv4() (string, error) {
	netName := c.cfg.Network
	if netName == "" {
		return "", fmt.Errorf("no network configured")
	}
	network, _, err := c.server.GetNetwork(netName)
	if err != nil {
		return "", err
	}
	cidr := network.Config["ipv4.address"]
	if cidr == "" || strings.EqualFold(cidr, "none") {
		return "", fmt.Errorf("network %q has no ipv4.address", netName)
	}
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("parse network cidr %q: %w", cidr, err)
	}

	used := map[string]struct{}{ip.String(): {}}
	leases, err := c.server.GetNetworkLeases(netName)
	if err == nil {
		for _, l := range leases {
			if l.Address != "" {
				used[l.Address] = struct{}{}
			}
		}
	}
	instances, err := c.server.GetInstances(api.InstanceTypeAny)
	if err == nil {
		for _, inst := range instances {
			for _, dev := range inst.Devices {
				if dev["type"] != "nic" {
					continue
				}
				addr := strings.Split(dev["ipv4.address"], "/")[0]
				if addr != "" {
					used[addr] = struct{}{}
				}
			}
			for _, dev := range inst.ExpandedDevices {
				if dev["type"] != "nic" {
					continue
				}
				addr := strings.Split(dev["ipv4.address"], "/")[0]
				if addr != "" {
					used[addr] = struct{}{}
				}
			}
		}
	}

	base := ipNet.IP.To4()
	if base == nil {
		return "", fmt.Errorf("not ipv4: %s", ip)
	}
	ones, bits := ipNet.Mask.Size()
	if bits != 32 || ones >= 30 {
		return "", fmt.Errorf("unsupported ipv4 mask for %q", cidr)
	}
	maxHosts := 1 << (32 - ones)
	netInt := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	for host := 2; host < maxHosts-1 && host < 250; host++ {
		candInt := netInt + uint32(host)
		cand := net.IPv4(byte(candInt>>24), byte(candInt>>16), byte(candInt>>8), byte(candInt)).String()
		if _, ok := used[cand]; ok {
			continue
		}
		return cand, nil
	}
	return "", fmt.Errorf("no free ipv4 addresses on %s", netName)
}

func maskToUint(mask net.IPMask) uint32 {
	if len(mask) != 4 {
		return 0
	}
	return uint32(mask[0])<<24 | uint32(mask[1])<<16 | uint32(mask[2])<<8 | uint32(mask[3])
}
