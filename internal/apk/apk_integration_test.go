package apk_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VizzleTF/owfeed/internal/apk"
	"github.com/VizzleTF/owfeed/internal/keys"
)

// This is the only test that proves owfeed can actually produce a signed index, so it
// runs the real chain end to end: fetch OpenWrt's signed checksum list, verify it under
// a pinned key from a second host, stream a 285 MB SDK, extract the toolchain, build a
// package, and sign an index with a key we generated.
//
// It needs network and (off linux/amd64) Docker, so it is opt-in:
//
//	OWFEED_INTEGRATION=1 go test ./internal/apk/ -run Integration -v
//
// The cache is reused between runs; point OWFEED_TEST_CACHE at a stable directory to
// avoid re-downloading the SDK every time.
const release = "25.12.5"

func testCache(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("OWFEED_TEST_CACHE"); dir != "" {
		return dir
	}
	return t.TempDir()
}

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("OWFEED_INTEGRATION") == "" {
		t.Skip("set OWFEED_INTEGRATION=1 to run (downloads an SDK, needs Docker off linux/amd64)")
	}
}

func TestIntegrationAcquireAndSign(t *testing.T) {
	requireIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	sdkDir, err := apk.Acquire(ctx, http.DefaultClient, testCache(t), release)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	tool, err := apk.Resolve(ctx, apk.Options{SDKDir: sdkDir, AllowContainer: true})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	t.Logf("using %s (%s)", tool.Version, tool.Origin)
	if !strings.HasPrefix(tool.Version, "apk-tools 3.") {
		t.Fatalf("version = %q, want apk-tools 3.x", tool.Version)
	}

	// A signing key, and the identity apk should record for it.
	key, err := keys.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	wantID, err := keys.IdentityOf(&key.PublicKey)
	if err != nil {
		t.Fatalf("IdentityOf: %v", err)
	}

	keyDir := t.TempDir()
	if err := keys.WritePrivate(filepath.Join(keyDir, "feed.pem"), key); err != nil {
		t.Fatalf("WritePrivate: %v", err)
	}

	// A minimal package payload.
	work := t.TempDir()
	payload := filepath.Join(work, "root", "usr", "bin")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "hello"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// arch is noarch, never "all" — apk rejects "all" as uninstallable.
	if _, err := tool.RunOK(ctx, apk.Invocation{
		Workdir: work,
		Args: []string{"mkpkg",
			"--info", "name:hello",
			"--info", "version:1.0-r1",
			"--info", "arch:noarch",
			"--info", "description:integration test package",
			"--files", "root",
			"--output", "hello-1.0-r1.apk",
		},
	}); err != nil {
		t.Fatalf("mkpkg: %v", err)
	}

	// mkndx runs with the publish dir as cwd and ./*.apk as arguments so no path
	// prefixes end up in the index. --allow-untrusted is required because the inputs
	// are our own unsigned packages, and it is the one place the flag is legitimate.
	if _, err := tool.RunOK(ctx, apk.Invocation{
		Workdir: work,
		KeyDir:  keyDir,
		Args: []string{"mkndx",
			"--allow-untrusted",
			"--sign-key", apk.KeyRef("feed.pem"),
			"--output", "packages.adb",
			"./hello-1.0-r1.apk",
		},
	}); err != nil {
		t.Fatalf("mkndx: %v", err)
	}

	// The index must be deflate-compressed ADB. zstd is accepted by a host apk built
	// with it and rejected on-device, since OpenWrt builds apk -Dzstd=disabled.
	adb, err := os.ReadFile(filepath.Join(work, "packages.adb"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if got := string(adb[:4]); got != "ADBd" {
		t.Errorf("index magic = %q, want ADBd (deflate)", got)
	}

	// The identity apk recorded must be the one owfeed computed. This is the assertion
	// that ties internal/keys to reality: if IdentityOf ever drifts, published feeds
	// would claim a key that subscribers cannot match.
	dump, err := tool.RunOK(ctx, apk.Invocation{Workdir: work, Args: []string{"adbdump", "packages.adb"}})
	if err != nil {
		t.Fatalf("adbdump: %v", err)
	}
	if !strings.Contains(dump.Stdout, wantID.String()) {
		t.Errorf("index does not carry key identity %s\n%s", wantID, dump.Stdout)
	}
}

// Acquire must publish nothing on failure. A half-extracted toolchain left in the cache
// would be picked up and trusted by every later run.
func TestIntegrationAcquireRejectsUnknownRelease(t *testing.T) {
	requireIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cache := t.TempDir()
	if _, err := apk.Acquire(ctx, http.DefaultClient, cache, "0.0.0"); err == nil {
		t.Fatal("Acquire(nonexistent release) succeeded")
	}

	entries, err := os.ReadDir(filepath.Join(cache, "sdk"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("failed Acquire left %s in the cache", e.Name())
		}
	}
}
