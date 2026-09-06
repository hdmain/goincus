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
chpasswd:
  expire: false
ssh_pwauth: true
disable_root: false
runcmd:
  - [ bash, -lc, "echo '%s' | base64 -d | passwd --stdin root 2>/dev/null || (echo 'root:'\"$(echo '%s' | base64 -d)\" | chpasswd)" ]
  - [ bash, -lc, "sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config" ]
  - [ bash, -lc, "sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config" ]
  - [ bash, -lc, "systemctl enable --now ssh 2>/dev/null || systemctl enable --now sshd 2>/dev/null || service ssh start || service sshd start || true" ]
`, b64, b64)
}

// EnsureSSH installs/configures OpenSSH inside a running container and sets the root password.
func (c *Client) EnsureSSH(name, rootPassword string) error {
	if rootPassword == "" {
		return fmt.Errorf("root password is empty")
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(rootPassword))
	script := fmt.Sprintf(`set -e
export DEBIAN_FRONTEND=noninteractive
if command -v apt-get >/dev/null 2>&1; then
  apt-get update -y
  apt-get install -y openssh-server
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y openssh-server
elif command -v apk >/dev/null 2>&1; then
  apk add --no-cache openssh
fi
PASS="$(echo '%s' | base64 -d)"
echo "root:${PASS}" | chpasswd
mkdir -p /etc/ssh
if [ -f /etc/ssh/sshd_config ]; then
  sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
  sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
  grep -q '^PermitRootLogin ' /etc/ssh/sshd_config || echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config
  grep -q '^PasswordAuthentication ' /etc/ssh/sshd_config || echo 'PasswordAuthentication yes' >> /etc/ssh/sshd_config
fi
systemctl enable ssh 2>/dev/null || systemctl enable sshd 2>/dev/null || true
systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || service ssh restart 2>/dev/null || service sshd restart 2>/dev/null || true
# Wait briefly for sshd to bind :22
for i in 1 2 3 4 5 6 7 8 9 10; do
  if ss -ltn 2>/dev/null | grep -q ':22' || netstat -ltn 2>/dev/null | grep -q ':22'; then
    exit 0
  fi
  sleep 1
done
exit 0
`, b64)

	var stdout, stderr bytes.Buffer
	req := api.InstanceExecPost{
		Command:     []string{"bash", "-lc", script},
		WaitForWS:   true,
		Interactive: false,
	}
	args := incus.InstanceExecArgs{
		Stdout: &stdout,
		Stderr: &stderr,
		Stdin:  strings.NewReader(""),
	}
	op, err := c.server.ExecInstance(name, req, &args)
	if err != nil {
		return fmt.Errorf("exec ensure ssh: %w", err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("ensure ssh failed: %w; stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	return nil
}

// WaitForSSHD polls until something listens on TCP/22 inside the container (best-effort).
func (c *Client) WaitForSSHD(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var stdout bytes.Buffer
		req := api.InstanceExecPost{
			Command:     []string{"bash", "-lc", "ss -ltn 2>/dev/null | grep -q ':22 ' || netstat -ltn 2>/dev/null | grep -q ':22 '"},
			WaitForWS:   true,
			Interactive: false,
		}
		args := incus.InstanceExecArgs{Stdout: &stdout, Stderr: &stdout, Stdin: strings.NewReader("")}
		op, err := c.server.ExecInstance(name, req, &args)
		if err == nil {
			if err := op.Wait(); err == nil {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("sshd did not become ready within %s", timeout)
}
