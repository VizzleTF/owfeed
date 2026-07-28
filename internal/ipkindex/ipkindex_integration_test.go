package ipkindex_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VizzleTF/owfeed/internal/ipk"
	"github.com/VizzleTF/owfeed/internal/ipkindex"
	"github.com/VizzleTF/owfeed/internal/usign"
)

// The only useful proof of an opkg feed is opkg using it, with signature checking
// left on. A feed that merely parses is one that fails on a stock router.
//
//	OWFEED_INTEGRATION=1 go test ./internal/ipkindex/ -run Integration -v
const image = "openwrt/rootfs:x86-64-24.10.8"

func TestIntegrationOpkgInstallsFromTheFeed(t *testing.T) {
	if os.Getenv("OWFEED_INTEGRATION") == "" {
		t.Skip("set OWFEED_INTEGRATION=1 to run (needs Docker)")
	}

	feed := t.TempDir()
	payload := t.TempDir()
	if err := os.MkdirAll(filepath.Join(payload, "etc", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "etc", "config", "demo"), []byte("config demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ipk.Build(ipk.Package{
		Name: "luci-app-demo", Version: "1.0.0-r1", Arch: ipk.ArchAll,
		Description: "A demo application.", Section: "luci",
		Conffiles: []string{"/etc/config/demo"},
	}, ipk.Options{Payload: payload, OutDir: feed, Epoch: time.Unix(1700000000, 0)}); err != nil {
		t.Fatal(err)
	}

	key, err := usign.Generate()
	if err != nil {
		t.Fatal(err)
	}
	res, err := ipkindex.Build(ipkindex.Options{Dir: feed, Key: key, Comment: "owfeed test feed"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Packages) != 1 {
		t.Fatalf("indexed %d packages, want 1", len(res.Packages))
	}

	keys := t.TempDir()
	// The filename IS the key id here — opkg looks the key up by it, unlike apk
	// which matches on the identity inside the signature and ignores the name.
	if err := os.WriteFile(filepath.Join(keys, key.ID.String()), key.MarshalPublic("owfeed test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The repository line names the directory: opkg appends Packages.gz itself.
	// apk's line names the index file. Each form is wrong for the other manager.
	script := `set -e
mkdir -p /var/lock /etc/opkg/keys
cp /keys/* /etc/opkg/keys/
echo "src/gz owfeed_test file:///feed" > /etc/opkg/customfeeds.conf
grep -q '^option check_signature' /etc/opkg.conf || { echo "signature checking is off; this proves nothing"; exit 1; }
opkg update
opkg install luci-app-demo
test -f /etc/config/demo
echo OPKG-FEED-OK
`
	out, err := exec.Command("docker", "run", "--rm", "--platform", "linux/amd64",
		"-v", feed+":/feed:ro", "-v", keys+":/keys:ro", image, "sh", "-c", script).CombinedOutput()
	t.Logf("opkg:\n%s", out)
	if err != nil {
		t.Fatalf("opkg could not use the feed: %v", err)
	}
	if !strings.Contains(string(out), "OPKG-FEED-OK") {
		t.Fatal("the check did not run to completion")
	}
}

// Without the key installed, opkg must refuse. Otherwise the signature is
// decoration and the feed is trusted by anyone who can answer its URL.
func TestIntegrationOpkgRefusesAnUntrustedFeed(t *testing.T) {
	if os.Getenv("OWFEED_INTEGRATION") == "" {
		t.Skip("set OWFEED_INTEGRATION=1 to run")
	}

	feed := t.TempDir()
	payload := t.TempDir()
	if err := os.WriteFile(filepath.Join(payload, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ipk.Build(ipk.Package{Name: "d", Version: "1.0-r1", Arch: ipk.ArchAll},
		ipk.Options{Payload: payload, OutDir: feed, Epoch: time.Unix(1700000000, 0)}); err != nil {
		t.Fatal(err)
	}
	key, err := usign.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ipkindex.Build(ipkindex.Options{Dir: feed, Key: key}); err != nil {
		t.Fatal(err)
	}

	script := `mkdir -p /var/lock
echo "src/gz owfeed_test file:///feed" > /etc/opkg/customfeeds.conf
opkg update 2>&1 | grep -q "Signature check failed" && echo REFUSED
`
	out, _ := exec.Command("docker", "run", "--rm", "--platform", "linux/amd64",
		"-v", feed+":/feed:ro", image, "sh", "-c", script).CombinedOutput()
	if !strings.Contains(string(out), "REFUSED") {
		t.Errorf("opkg accepted a feed whose key it does not have:\n%s", out)
	}
}
