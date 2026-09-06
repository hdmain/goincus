package incusclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePackagesAndResolveOpenSSH(t *testing.T) {
	idx, err := parsePackages(strings.NewReader(`
Package: openssh-server
Version: 1:9.6p1-3ubuntu13
Filename: pool/main/o/openssh/openssh-server_9.6p1-3ubuntu13_amd64.deb
Depends: openssh-client (= 1:9.6p1-3ubuntu13), openssh-sftp-server, libwrap0 (>= 7.6-4~), adduser

Package: openssh-client
Version: 1:9.6p1-3ubuntu13
Filename: pool/main/o/openssh/openssh-client_9.6p1-3ubuntu13_amd64.deb
Depends: libc6, adduser

Package: openssh-sftp-server
Version: 1:9.6p1-3ubuntu13
Filename: pool/main/o/openssh/openssh-sftp-server_9.6p1-3ubuntu13_amd64.deb
Depends: openssh-client (= 1:9.6p1-3ubuntu13)

Package: libwrap0
Version: 7.6.q-33
Filename: pool/main/t/tcp-wrappers/libwrap0_7.6.q-33_amd64.deb
Depends: libc6
`))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := resolveOpenSSHDebs(idx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range pkgs {
		got[p.Name] = true
	}
	for _, need := range []string{"openssh-server", "openssh-client", "openssh-sftp-server", "libwrap0"} {
		if !got[need] {
			t.Fatalf("missing %s in %#v", need, got)
		}
	}
	if got["adduser"] || got["libc6"] {
		t.Fatalf("should skip base packages, got %#v", got)
	}
}

func TestParseDepNames(t *testing.T) {
	names := parseDepNames("openssh-client (= 1:9.6), libwrap0 (>= 7.6-4~) | libwrap0t64, adduser")
	if len(names) < 3 || names[0] != "openssh-client" || names[1] != "libwrap0" || names[2] != "adduser" {
		t.Fatalf("unexpected %#v", names)
	}
}

func TestEnsureOpenSSHDebsLive(t *testing.T) {
	if os.Getenv("GOINCUS_LIVE") == "" {
		t.Skip("set GOINCUS_LIVE=1 to hit Ubuntu mirrors")
	}
	dir := t.TempDir()
	// Point cache under temp by using ubuntu-noble-amd64 layout manually via EnsureOpenSSHDebs
	// which writes to /var/cache/... — override by testing load+resolve+download helpers.
	g := guestOS{ID: "ubuntu", Codename: "noble", Arch: "amd64"}
	index, err := loadPackagesIndex(g)
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := resolveOpenSSHDebs(index)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := guestMirrorRoots(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		if err := downloadDebFile(roots, pkg, dir); err != nil {
			t.Fatal(err)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "openssh-server_*.deb"))
	if len(matches) == 0 {
		t.Fatal("no openssh-server deb downloaded")
	}
}
