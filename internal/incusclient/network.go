package incusclient

import (
	"fmt"
	"net"
	"strings"

	"github.com/lxc/incus/v6/shared/api"
)

const defaultBridgeCIDR = "10.72.160.1/24"

// HardenNetwork ensures NAT + DHCP + a concrete IPv4 CIDR on a managed bridge.
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

	addr := strings.TrimSpace(n.Config["ipv4.address"])
	if addr == "" || strings.EqualFold(addr, "none") || strings.EqualFold(addr, "auto") {
		set("ipv4.address", defaultBridgeCIDR)
	} else if _, _, err := net.ParseCIDR(addr); err != nil {
		set("ipv4.address", defaultBridgeCIDR)
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

// NetworkIPv4CIDR returns the managed bridge IPv4 CIDR (gateway/mask).
func (c *Client) NetworkIPv4CIDR() (string, error) {
	netName := c.cfg.Network
	if netName == "" {
		return "", fmt.Errorf("no network configured")
	}
	if err := c.HardenNetwork(netName); err != nil {
		return "", err
	}
	n, _, err := c.server.GetNetwork(netName)
	if err != nil {
		return "", err
	}
	cidr := strings.TrimSpace(n.Config["ipv4.address"])
	if _, _, err := net.ParseCIDR(cidr); err != nil {
		return "", fmt.Errorf("network %q has unusable ipv4.address %q", netName, cidr)
	}
	return cidr, nil
}

// AllocateContainerIPv4 picks a free address from the managed bridge pool.
func (c *Client) AllocateContainerIPv4() (string, error) {
	netName := c.cfg.Network
	if netName == "" {
		return "", fmt.Errorf("no network configured")
	}
	cidr, err := c.NetworkIPv4CIDR()
	if err != nil {
		return "", err
	}
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("parse network cidr %q: %w", cidr, err)
	}

	used := map[string]struct{}{}
	if gw, _, err := parseHostFromCIDR(cidr); err == nil {
		used[gw] = struct{}{}
	}
	if leases, err := c.server.GetNetworkLeases(netName); err == nil {
		for _, l := range leases {
			if l.Address != "" {
				used[l.Address] = struct{}{}
			}
		}
	}
	if instances, err := c.server.GetInstances(api.InstanceTypeAny); err == nil {
		for _, inst := range instances {
			for _, devices := range []map[string]map[string]string{inst.Devices, inst.ExpandedDevices} {
				for _, dev := range devices {
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
	}

	base := ipNet.IP.To4()
	if base == nil {
		return "", fmt.Errorf("not ipv4 network: %s", cidr)
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

func ipv4Netmask(prefixLen string) string {
	var bits int
	if _, err := fmt.Sscanf(prefixLen, "%d", &bits); err != nil || bits < 0 || bits > 32 {
		return "255.255.255.0"
	}
	mask := net.CIDRMask(bits, 32)
	return net.IP(mask).String()
}

// EnsureInstanceIPv4 assigns a static IPv4 on the instance NIC and configures it inside the guest
// so `incus list` shows an address (DHCP alone is unreliable on minimal images).
func (c *Client) EnsureInstanceIPv4(name string) (string, error) {
	if err := c.EnsureInfrastructure(); err != nil {
		return "", err
	}
	cidr, err := c.NetworkIPv4CIDR()
	if err != nil {
		return "", err
	}

	inst, etag, err := c.server.GetInstance(name)
	if err != nil {
		return "", err
	}
	if inst.Devices == nil {
		inst.Devices = map[string]map[string]string{}
	}

	eth0, hadDevice := inst.Devices["eth0"]
	if !hadDevice {
		eth0 = map[string]string{}
		if expanded := inst.ExpandedDevices["eth0"]; expanded != nil {
			for k, v := range expanded {
				eth0[k] = v
			}
		}
	}
	eth0["type"] = "nic"
	if eth0["network"] == "" {
		eth0["network"] = c.cfg.Network
	}
	if eth0["name"] == "" {
		eth0["name"] = "eth0"
	}

	ip := strings.Split(strings.TrimSpace(eth0["ipv4.address"]), "/")[0]
	needUpdate := !hadDevice
	if ip == "" || net.ParseIP(ip) == nil {
		allocated, err := c.AllocateContainerIPv4()
		if err != nil {
			return "", err
		}
		ip = allocated
		eth0["ipv4.address"] = ip
		needUpdate = true
	} else {
		eth0["ipv4.address"] = ip
	}

	if needUpdate {
		inst.Devices["eth0"] = eth0
		op, err := c.server.UpdateInstance(name, inst.Writable(), etag)
		if err != nil {
			return "", fmt.Errorf("set eth0 ipv4.address: %w", err)
		}
		if err := op.Wait(); err != nil {
			return "", fmt.Errorf("set eth0 ipv4.address: %w", err)
		}
	}

	if err := c.ApplyGuestStaticIPv4(name, ip, cidr); err != nil {
		return "", err
	}
	return ip, nil
}

// ApplyGuestStaticIPv4 configures a persistent static address inside the container.
func (c *Client) ApplyGuestStaticIPv4(name, ip, cidr string) error {
	gw, mask, err := parseHostFromCIDR(cidr)
	if err != nil {
		return err
	}
	netmask := ipv4Netmask(mask)
	script := fmt.Sprintf(`set -eux
IP=%q
GW=%q
MASK=%q
NETMASK=%q
IFACE=""
for cand in eth0 enp0s3; do
  if ip link show "$cand" >/dev/null 2>&1; then
    IFACE="$cand"
    break
  fi
done
if [ -z "$IFACE" ]; then
  IFACE="$(ip -o link show | awk -F': ' '$2!="lo"{print $2; exit}')"
fi
test -n "$IFACE"

ip link set "$IFACE" up || true
ip -4 addr flush dev "$IFACE" || true
ip addr add "$IP/$MASK" dev "$IFACE" || ip addr replace "$IP/$MASK" dev "$IFACE"
ip route replace default via "$GW" dev "$IFACE" || true

umount /etc/resolv.conf 2>/dev/null || true
printf 'nameserver 1.1.1.1\nnameserver 8.8.8.8\nnameserver %%s\n' "$GW" > /etc/resolv.conf

if command -v netplan >/dev/null 2>&1 || [ -d /etc/netplan ]; then
  mkdir -p /etc/netplan
  cat > /etc/netplan/99-goincus.yaml <<EOF
network:
  version: 2
  renderer: networkd
  ethernets:
    $IFACE:
      dhcp4: false
      dhcp6: false
      addresses: [$IP/$MASK]
      routes:
        - to: default
          via: $GW
      nameservers:
        addresses: [1.1.1.1, 8.8.8.8, $GW]
EOF
  chmod 600 /etc/netplan/99-goincus.yaml
  netplan apply 2>/dev/null || true
fi

if [ -d /etc/network ]; then
  mkdir -p /etc/network/interfaces.d
  cat > /etc/network/interfaces.d/99-goincus <<EOF
auto $IFACE
iface $IFACE inet static
    address $IP
    netmask $NETMASK
    gateway $GW
    dns-nameservers 1.1.1.1 8.8.8.8 $GW
EOF
fi

if [ -d /etc/systemd/network ] || command -v networkctl >/dev/null 2>&1; then
  mkdir -p /etc/systemd/network
  cat > /etc/systemd/network/10-goincus.network <<EOF
[Match]
Name=$IFACE

[Network]
Address=$IP/$MASK
Gateway=$GW
DNS=1.1.1.1
DNS=8.8.8.8
DNS=$GW
EOF
  systemctl restart systemd-networkd 2>/dev/null || true
fi

ip -4 addr show dev "$IFACE" | grep -F "$IP"
`, ip, gw, mask, netmask)

	stdout, stderr, code, err := c.exec(name, script)
	if err != nil {
		return fmt.Errorf("apply guest ipv4: %w (%s %s)", err, stdout, stderr)
	}
	if code != 0 {
		return fmt.Errorf("apply guest ipv4 exited %d (%s %s)", code, stdout, stderr)
	}
	return nil
}
