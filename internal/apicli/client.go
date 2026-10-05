package apicli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hdmain/goincus/internal/models"
	"github.com/hdmain/goincus/internal/setup"
)

// Client talks to the goincus HTTP API.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// Options configures a CLI API client.
type Options struct {
	URL        string
	APIKey     string
	ConfigPath string
}

// New builds a client, filling URL/key from flags, env, or local config.
func New(opts Options) (*Client, error) {
	base := strings.TrimSpace(opts.URL)
	if base == "" {
		base = strings.TrimSpace(os.Getenv("GOINCUS_URL"))
	}
	if base == "" {
		base = "http://127.0.0.1:9603"
	}
	base = strings.TrimRight(base, "/")

	key := strings.TrimSpace(opts.APIKey)
	if key == "" {
		key = strings.TrimSpace(os.Getenv("GOINCUS_API_KEY"))
	}

	cfgPath := strings.TrimSpace(opts.ConfigPath)
	if cfgPath == "" {
		cfgPath = strings.TrimSpace(os.Getenv("GOINCUS_CONFIG"))
	}
	if cfgPath == "" {
		cfgPath = setup.DefaultConfigPath
	}

	if key == "" {
		if k, err := apiKeyFromConfig(cfgPath); err == nil && k != "" {
			key = k
		}
	}
	if key == "" {
		return nil, fmt.Errorf("API key required: pass -key, set GOINCUS_API_KEY, or use %s", cfgPath)
	}

	return &Client{
		BaseURL: base,
		APIKey:  key,
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

func apiKeyFromConfig(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Auth struct {
			APIKeys []string `yaml:"api_keys"`
		} `yaml:"auth"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	for _, k := range cfg.Auth.APIKeys {
		k = strings.TrimSpace(k)
		if k != "" && !strings.HasPrefix(k, "REPLACE_") {
			return k, nil
		}
	}
	return "", fmt.Errorf("no api_keys in %s", path)
}

// Health GET /healthz
func (c *Client) Health(ctx context.Context) (*models.HealthResponse, error) {
	var out models.HealthResponse
	if err := c.do(ctx, http.MethodGet, "/healthz", nil, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListInstances GET /api/v1/instances/
func (c *Client) ListInstances(ctx context.Context) ([]models.Instance, error) {
	var out []models.Instance
	if err := c.do(ctx, http.MethodGet, "/api/v1/instances/", nil, &out, true); err != nil {
		return nil, err
	}
	if out == nil {
		out = []models.Instance{}
	}
	return out, nil
}

// GetInstance GET /api/v1/instances/{id}
func (c *Client) GetInstance(ctx context.Context, idOrName string) (*models.Instance, error) {
	var out models.Instance
	path := "/api/v1/instances/" + url.PathEscape(idOrName)
	if err := c.do(ctx, http.MethodGet, path, nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetInstanceResources GET /api/v1/instances/{id}/resources
func (c *Client) GetInstanceResources(ctx context.Context, idOrName string) (*models.InstanceResourcesResponse, error) {
	var out models.InstanceResourcesResponse
	path := "/api/v1/instances/" + url.PathEscape(idOrName) + "/resources"
	if err := c.do(ctx, http.MethodGet, path, nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetInstanceUsage GET /api/v1/instances/{id}/usage?days=N
func (c *Client) GetInstanceUsage(ctx context.Context, idOrName string, days int) (*models.UsageChartResponse, error) {
	var out models.UsageChartResponse
	path := "/api/v1/instances/" + url.PathEscape(idOrName) + "/usage"
	if days > 0 {
		path += "?days=" + fmt.Sprintf("%d", days)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetMetrics GET /api/v1/metrics?hours=N (all instances) or /instances/{id}/metrics.
func (c *Client) GetMetrics(ctx context.Context, idOrName string, hours int) (*models.MetricsChartResponse, error) {
	var out models.MetricsChartResponse
	var path string
	if strings.TrimSpace(idOrName) == "" {
		path = "/api/v1/metrics"
	} else {
		path = "/api/v1/instances/" + url.PathEscape(idOrName) + "/metrics"
	}
	if hours > 0 {
		path += "?hours=" + fmt.Sprintf("%d", hours)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetHostStats GET /api/v1/hoststats
func (c *Client) GetHostStats(ctx context.Context) (*models.HostStatsResponse, error) {
	var out models.HostStatsResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/hoststats", nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateInstance POST /api/v1/instances/
func (c *Client) CreateInstance(ctx context.Context, req models.CreateInstanceRequest) (*models.Instance, error) {
	var out models.Instance
	if err := c.do(ctx, http.MethodPost, "/api/v1/instances/", req, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteInstance DELETE /api/v1/instances/{id}
func (c *Client) DeleteInstance(ctx context.Context, idOrName string) error {
	path := "/api/v1/instances/" + url.PathEscape(idOrName)
	return c.do(ctx, http.MethodDelete, path, nil, nil, true)
}

// StartInstance POST /api/v1/instances/{id}/start
func (c *Client) StartInstance(ctx context.Context, idOrName string) (*models.Instance, error) {
	return c.action(ctx, idOrName, "start")
}

// StopInstance POST /api/v1/instances/{id}/stop
func (c *Client) StopInstance(ctx context.Context, idOrName string) (*models.Instance, error) {
	return c.action(ctx, idOrName, "stop")
}

// RestartInstance POST /api/v1/instances/{id}/restart
func (c *Client) RestartInstance(ctx context.Context, idOrName string) (*models.Instance, error) {
	return c.action(ctx, idOrName, "restart")
}

// RepairInstance POST /api/v1/instances/{id}/repair
func (c *Client) RepairInstance(ctx context.Context, idOrName string) (*models.Instance, error) {
	return c.action(ctx, idOrName, "repair")
}

func (c *Client) action(ctx context.Context, idOrName, action string) (*models.Instance, error) {
	var out models.Instance
	path := fmt.Sprintf("/api/v1/instances/%s/%s", url.PathEscape(idOrName), action)
	if err := c.do(ctx, http.MethodPost, path, nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// WaitRunning polls until status is running with at least one port, or error/timeout.
func (c *Client) WaitRunning(ctx context.Context, idOrName string, timeout time.Duration) (*models.Instance, error) {
	deadline := time.Now().Add(timeout)
	var last *models.Instance
	for time.Now().Before(deadline) {
		inst, err := c.GetInstance(ctx, idOrName)
		if err != nil {
			return nil, err
		}
		last = inst
		switch inst.Status {
		case models.StatusRunning:
			if len(inst.Ports) > 0 {
				return inst, nil
			}
		case models.StatusError:
			msg := inst.ErrorMessage
			if msg == "" {
				msg = "error"
			}
			return inst, fmt.Errorf("instance %s failed: %s", inst.Name, msg)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last != nil {
		return last, fmt.Errorf("timeout waiting for %s (status=%s ports=%d)", idOrName, last.Status, len(last.Ports))
	}
	return nil, fmt.Errorf("timeout waiting for %s", idOrName)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any, auth bool) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNoContent || len(raw) == 0 {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		return fmt.Errorf("HTTP %d %s %s", resp.StatusCode, method, path)
	}
	if resp.StatusCode >= 300 {
		var er models.ErrorResponse
		if json.Unmarshal(raw, &er) == nil && er.Error != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, er.Error)
		}
		return fmt.Errorf("HTTP %d %s %s: %s", resp.StatusCode, method, path, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// SSHPort returns the lowest host port (sshd listen port) or 0.
func SSHPort(inst *models.Instance) int {
	if inst == nil || len(inst.Ports) == 0 {
		return 0
	}
	min := inst.Ports[0].HostPort
	for _, p := range inst.Ports[1:] {
		if p.HostPort < min {
			min = p.HostPort
		}
	}
	return min
}

// PortRange formats contiguous host ports as "N-M" or "N".
func PortRange(inst *models.Instance) string {
	if inst == nil || len(inst.Ports) == 0 {
		return "-"
	}
	min, max := inst.Ports[0].HostPort, inst.Ports[0].HostPort
	for _, p := range inst.Ports[1:] {
		if p.HostPort < min {
			min = p.HostPort
		}
		if p.HostPort > max {
			max = p.HostPort
		}
	}
	if min == max {
		return fmt.Sprintf("%d", min)
	}
	return fmt.Sprintf("%d-%d", min, max)
}
