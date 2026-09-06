//go:build linux

package setup

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hdmain/goincus/internal/config"
)

type redisEndpoint struct {
	Host     string
	Port     int
	Password string
	Source   string
}

var (
	redisPortRe     = regexp.MustCompile(`(?i)^\s*port\s+(\d+)\s*$`)
	redisPassRe     = regexp.MustCompile(`(?i)^\s*requirepass\s+(\S+)\s*$`)
	redisBindRe     = regexp.MustCompile(`(?i)^\s*bind\s+(.+)$`)
	ssListenRedisRe = regexp.MustCompile(`(?i)redis`)
	ssPortRe        = regexp.MustCompile(`:(\d+)\s`)
)

// detectExistingRedis finds a usable local Redis instance on any port.
func detectExistingRedis() (*redisEndpoint, bool) {
	candidates := collectRedisPortCandidates()
	for _, port := range candidates {
		if ep, ok := probeRedis("127.0.0.1", port, ""); ok {
			return ep, true
		}
		// Retry with passwords discovered from config files.
		for _, pass := range collectRedisPasswords() {
			if ep, ok := probeRedis("127.0.0.1", port, pass); ok {
				return ep, true
			}
		}
	}

	// Config-only fallback: redis is installed but not yet listening.
	if ep, ok := redisFromConfigFiles(); ok {
		if _, err := exec.LookPath("redis-server"); err == nil || _, err2 := exec.LookPath("redis-cli"); err2 == nil {
			return ep, true
		}
	}
	return nil, false
}

func collectRedisPortCandidates() []int {
	seen := map[int]struct{}{}
	var ports []int
	add := func(p int) {
		if p <= 0 || p > 65535 {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		ports = append(ports, p)
	}

	// Prefer ports already listening for redis processes.
	for _, p := range redisListeningPorts() {
		add(p)
	}
	// Then ports declared in config.
	if ep, ok := redisFromConfigFiles(); ok {
		add(ep.Port)
	}
	// Common defaults last.
	add(6379)
	add(9602)
	return ports
}

func collectRedisPasswords() []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(p string) {
		p = strings.Trim(p, `"'`)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if ep, ok := redisFromConfigFiles(); ok {
		add(ep.Password)
	}
	return out
}

func redisListeningPorts() []int {
	out, err := exec.Command("ss", "-ltnp").CombinedOutput()
	if err != nil {
		return nil
	}
	var ports []int
	seen := map[int]struct{}{}
	for _, line := range strings.Split(string(out), "\n") {
		if !ssListenRedisRe.MatchString(line) {
			continue
		}
		for _, m := range ssPortRe.FindAllStringSubmatch(line, -1) {
			p, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			ports = append(ports, p)
		}
	}
	return ports
}

func redisFromConfigFiles() (*redisEndpoint, bool) {
	paths := []string{
		"/etc/redis/redis.conf",
		"/etc/redis.conf",
		"/etc/redis/goincus.conf",
	}
	// Include drop-ins under /etc/redis/
	if entries, err := os.ReadDir("/etc/redis"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".conf") {
				paths = append(paths, "/etc/redis/"+name)
			}
		}
	}

	ep := &redisEndpoint{Host: "127.0.0.1", Port: 6379, Source: "config"}
	found := false
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		foundFile := false
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if m := redisPortRe.FindStringSubmatch(line); len(m) == 2 {
				if p, err := strconv.Atoi(m[1]); err == nil {
					ep.Port = p
					foundFile = true
				}
			}
			if m := redisPassRe.FindStringSubmatch(line); len(m) == 2 {
				ep.Password = strings.Trim(m[1], `"'`)
				foundFile = true
			}
			if m := redisBindRe.FindStringSubmatch(line); len(m) == 2 {
				fields := strings.Fields(m[1])
				for _, b := range fields {
					if b == "127.0.0.1" || b == "0.0.0.0" || b == "::1" || b == "*" {
						ep.Host = "127.0.0.1"
						foundFile = true
						break
					}
					if ip := net.ParseIP(b); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
						ep.Host = "127.0.0.1"
						foundFile = true
						break
					}
				}
			}
		}
		_ = f.Close()
		if foundFile {
			found = true
		}
	}
	if !found {
		// Binary present with no readable conf still counts as "redis exists" only via probe.
		return nil, false
	}
	return ep, true
}

func probeRedis(host string, port int, password string) (*redisEndpoint, bool) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 400*time.Millisecond)
	if err != nil {
		return nil, false
	}
	_ = conn.Close()

	cli, err := exec.LookPath("redis-cli")
	if err != nil {
		// Port is open; accept without AUTH verification.
		return &redisEndpoint{Host: host, Port: port, Password: password, Source: "listening"}, true
	}

	args := []string{"-h", host, "-p", strconv.Itoa(port)}
	if password != "" {
		args = append(args, "-a", password, "--no-auth-warning")
	}
	args = append(args, "PING")
	cmd := exec.Command(cli, args...)
	out, err := cmd.CombinedOutput()
	resp := strings.TrimSpace(string(out))
	if err == nil && strings.EqualFold(resp, "PONG") {
		src := "running"
		if password != "" {
			src = "running+auth"
		}
		return &redisEndpoint{Host: host, Port: port, Password: password, Source: src}, true
	}

	// NOAUTH / WRONGPASS: port is Redis but auth required and unknown.
	lower := strings.ToLower(resp)
	if strings.Contains(lower, "noauth") || strings.Contains(lower, "authentication required") {
		return &redisEndpoint{Host: host, Port: port, Password: password, Source: "running-needs-auth"}, true
	}
	return nil, false
}

// applyExistingRedis updates cfg to point at a discovered Redis instance.
func applyExistingRedis(cfg *config.Config, ep *redisEndpoint) {
	cfg.Redis.Host = ep.Host
	cfg.Redis.Port = ep.Port
	// Keep discovered password; if empty, clear generated one so we don't invent a requirepass.
	cfg.Redis.Password = ep.Password
}

func redisBinaryPresent() bool {
	if _, err := exec.LookPath("redis-server"); err == nil {
		return true
	}
	if _, err := exec.LookPath("redis-cli"); err == nil {
		return true
	}
	return false
}

func formatRedisAddr(cfg *config.Config) string {
	return fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port)
}
