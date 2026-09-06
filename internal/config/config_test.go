package config

import "testing"

func TestDefaultPorts(t *testing.T) {
	cfg := Default()
	cfg.Auth.APIKeys = []string{"gic_test"}
	if cfg.Server.Port != 9603 {
		t.Fatalf("server port = %d, want 9603", cfg.Server.Port)
	}
	if cfg.Database.Host != "127.0.0.1" || cfg.Database.Port != 9601 {
		t.Fatalf("database = %s:%d, want 127.0.0.1:9601", cfg.Database.Host, cfg.Database.Port)
	}
	if cfg.Redis.Host != "127.0.0.1" || cfg.Redis.Port != 9602 {
		t.Fatalf("redis = %s:%d, want 127.0.0.1:9602", cfg.Redis.Host, cfg.Redis.Port)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsBadPortRange(t *testing.T) {
	cfg := Default()
	cfg.Auth.APIKeys = []string{"gic_test"}
	cfg.Ports.HostRangeStart = 30000
	cfg.Ports.HostRangeEnd = 20000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestGenerateInitialized(t *testing.T) {
	cfg, err := GenerateInitialized()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Password == "" || cfg.Redis.Password == "" {
		t.Fatal("expected generated passwords")
	}
	if len(cfg.Auth.APIKeys) < 2 {
		t.Fatalf("expected 2 api keys, got %d", len(cfg.Auth.APIKeys))
	}
}
