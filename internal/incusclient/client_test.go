package incusclient

import "testing"

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

