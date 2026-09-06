package secrets

import "testing"

func TestRandomPasswordAndAPIKey(t *testing.T) {
	pw, err := RandomPassword(24)
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) < 20 {
		t.Fatalf("password too short: %q", pw)
	}
	key, err := RandomAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(key) < 10 || key[:4] != "gic_" {
		t.Fatalf("unexpected api key: %q", key)
	}
}
