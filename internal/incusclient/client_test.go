package incusclient

import (
	"strings"
	"testing"
)

func TestResourceConfig(t *testing.T) {
	cfg := ResourceConfig(2, 1024, 256)
	checks := map[string]string{
		"limits.cpu":       "2",
		"limits.memory":    "1024MiB",
		"limits.processes": "256",
	}
	for k, want := range checks {
		if got := cfg[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if _, ok := cfg["limits.cpu.allowance"]; ok {
		t.Fatal("whole cores must not set limits.cpu.allowance")
	}
}

func TestResourceConfigHalfCPU(t *testing.T) {
	cfg := ResourceConfig(0.5, 256, 256)
	if cfg["limits.cpu"] != "1" {
		t.Fatalf("pin = %q, want 1", cfg["limits.cpu"])
	}
	if cfg["limits.cpu.allowance"] != "50ms/100ms" {
		t.Fatalf("allowance = %q, want 50ms/100ms", cfg["limits.cpu.allowance"])
	}
}

func TestResourceConfigOneAndHalf(t *testing.T) {
	cfg := ResourceConfig(1.5, 512, 256)
	if cfg["limits.cpu"] != "2" {
		t.Fatalf("pin = %q, want 2", cfg["limits.cpu"])
	}
	if cfg["limits.cpu.allowance"] != "150ms/100ms" {
		t.Fatalf("allowance = %q, want 150ms/100ms", cfg["limits.cpu.allowance"])
	}
}

func TestHardenedInstanceConfig(t *testing.T) {
	cfg := HardenedInstanceConfig()
	must := map[string]string{
		"security.privileged":     "false",
		"security.nesting":        "false",
		"security.idmap.isolated": "true",
		"security.guestapi":       "false",
	}
	for k, want := range must {
		if got := cfg[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.Contains(cfg["raw.lxc"], "tmpfs sys/block tmpfs") {
		t.Fatal("raw.lxc must hide /sys/block from guests")
	}
}

func TestMergeDiskIsolationRawLXC(t *testing.T) {
	got := MergeDiskIsolationRawLXC("lxc.apparmor.profile = unconfined\n")
	if !strings.Contains(got, "tmpfs sys/block tmpfs") || !strings.Contains(got, "apparmor") {
		t.Fatalf("merge failed: %q", got)
	}
	again := MergeDiskIsolationRawLXC(got)
	if strings.Count(again, "tmpfs sys/block tmpfs") != 1 {
		t.Fatalf("duplicate mount entries: %q", again)
	}
}

func TestApplyNICBandwidth(t *testing.T) {
	nic := map[string]string{
		"type":            "nic",
		"limits.ingress":  "1Gbit",
		"limits.egress":   "1Gbit",
	}
	ApplyNICBandwidth(nic, 100)
	if nic["limits.max"] != "100Mbit" {
		t.Fatalf("limits.max = %q, want 100Mbit", nic["limits.max"])
	}
	if _, ok := nic["limits.ingress"]; ok {
		t.Fatal("limits.ingress should be cleared when using limits.max")
	}
	ApplyNICBandwidth(nic, 0)
	if _, ok := nic["limits.max"]; ok {
		t.Fatal("limits.max should be cleared for unlimited")
	}
}

func TestSanitizeProfiles(t *testing.T) {
	got := sanitizeProfiles([]string{"default", "goincus-unprivileged", "other"})
	if len(got) < 1 || got[0] != UnprivilegedProfile {
		t.Fatalf("expected hardened profile first, got %#v", got)
	}
	for _, p := range got {
		if p == "default" {
			t.Fatal("default profile must be stripped")
		}
	}
}

func TestIsDFIsolatingDriver(t *testing.T) {
	if !isDFIsolatingDriver("lvm") || !isDFIsolatingDriver("zfs") {
		t.Fatal("lvm/zfs must isolate df")
	}
	if isDFIsolatingDriver("dir") || isDFIsolatingDriver("btrfs") {
		t.Fatal("dir/btrfs must not count as df-isolating")
	}
}
