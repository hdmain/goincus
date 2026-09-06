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

func TestHardenedNIC(t *testing.T) {
	nic := HardenedNIC("incusbr0", "10.72.160.5")
	if nic["security.mac_filtering"] != "true" || nic["security.port_isolation"] != "true" {
		t.Fatalf("expected NIC filtering, got %#v", nic)
	}
}
