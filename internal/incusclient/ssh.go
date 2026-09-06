package incusclient

import (
	"bytes"
	"encoding/base64"
	"fmt"
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
manage_resolv_conf: true
resolv_conf:
  nameservers: ['1.1.1.1', '8.8.8.8']
runcmd:
  - [ bash, -lc, "PASS=$(echo '%s' | base64 -d); echo root:$PASS | chpasswd; unset PASS" ]
  - [ bash, -lc, "ssh-keygen -A" ]
  - [ bash, -lc, "mkdir -p /etc/ssh/sshd_config.d; printf '%%s\\n' 'PermitRootLogin yes' 'PasswordAuthentication yes' 'AddressFamily inet' 'ListenAddress 0.0.0.0' 'ListenAddress 127.0.0.1' 'MaxAuthTries 4' 'LoginGraceTime 30' 'PermitEmptyPasswords no' 'X11Forwarding no' > /etc/ssh/sshd_config.d/99-goincus.conf" ]
  - [ bash, -lc, "systemctl disable --now ssh.socket 2>/dev/null || true; systemctl enable ssh 2>/dev/null || systemctl enable sshd 2>/dev/null || true; systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || true" ]
`, b64)
}

// EnsureSSH installs/configures OpenSSH inside a running container and sets the root password.
// It prefers apt when the guest has DNS, otherwise installs .debs pushed from the host.
func (c *Client) EnsureSSH(name, rootPassword string) error {
	if rootPassword == "" {
		return fmt.Errorf("root password is empty")
	}

	_ = c.ConfigureGuestDNS(name)

	// Try network install first; fall back to host-pushed packages (guest DNS often broken).
	aptErr := c.installOpenSSHViaApt(name)
	if aptErr != nil {
		if pushErr := c.InstallOpenSSHFromHost(name); pushErr != nil {
			return fmt.Errorf("install openssh (apt: %v; host-debs: %w)", aptErr, pushErr)
		}
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(rootPassword))
	// set -eu without -x so the root password is never echoed into journals/error_message.
	script := fmt.Sprintf(`set -eu
PASS="$(echo '%s' | base64 -d)"
echo "root:${PASS}" | chpasswd
unset PASS
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
MaxAuthTries 4
LoginGraceTime 30
MaxStartups 10:30:60
ClientAliveInterval 30
ClientAliveCountMax 3
X11Forwarding no
AllowTcpForwarding no
PermitEmptyPasswords no
EOF
systemctl disable --now ssh.socket 2>/dev/null || true
systemctl disable --now sshd.socket 2>/dev/null || true
systemctl unmask ssh 2>/dev/null || true
systemctl unmask sshd 2>/dev/null || true
systemctl enable ssh 2>/dev/null || systemctl enable sshd 2>/dev/null || true
systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || service ssh restart 2>/dev/null || service sshd restart 2>/dev/null || /usr/sbin/sshd || true
sshd -t || /usr/sbin/sshd -t || true
for i in $(seq 1 30); do
  if ss -ltn | grep -E '[:.]22[[:space:]]' >/dev/null 2>&1; then
    exit 0
  fi
  if [ "$i" = "5" ] || [ "$i" = "15" ]; then
    /usr/sbin/sshd || true
  fi
  sleep 1
done
echo "sshd failed to listen on :22" >&2
ss -ltn || true
systemctl status ssh --no-pager 2>/dev/null || systemctl status sshd --no-pager 2>/dev/null || true
exit 1
`, b64)

	stdout, stderr, code, err := c.exec(name, script)
	if err != nil {
		return fmt.Errorf("configure ssh: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("configure ssh exited %d", code)
	}
	if err := c.WaitForSSHD(name, 90*time.Second); err != nil {
		return err
	}
	_ = stdout
	_ = stderr
	return nil
}

func (c *Client) installOpenSSHViaApt(name string) error {
	stdout, stderr, code, err := c.exec(name, `
set -eux
export DEBIAN_FRONTEND=noninteractive
getent hosts archive.ubuntu.com >/dev/null || getent hosts deb.debian.org >/dev/null
if command -v apt-get >/dev/null 2>&1; then
  apt-get update -y
  apt-get install -y openssh-server openssh-client iproute2
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y openssh-server iproute
elif command -v apk >/dev/null 2>&1; then
  apk add --no-cache openssh openssh-server iproute2
else
  exit 1
fi
command -v sshd >/dev/null || test -x /usr/sbin/sshd
`)
	if err != nil {
		return fmt.Errorf("%w (%s %s)", err, stdout, stderr)
	}
	if code != 0 {
		return fmt.Errorf("exit %d (%s %s)", code, stdout, stderr)
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
