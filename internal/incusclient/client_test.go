package incusclient

import "testing"

func TestResourceConfig(t *testing.T) {
	cfg := ResourceConfig(2, 1024, 256)
	checks := map[string]string{
		"limits.cpu":          "2",
		"limits.memory":       "1024MiB",
		"limits.processes":    "256",
		"security.privileged": "false",
		"security.nesting":    "false",
	}
	for k, want := range checks {
		if got := cfg[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if _, ok := cfg["security.idmap.isolated"]; ok {
		t.Fatal("security.idmap.isolated should not be set by default")
	}
}
