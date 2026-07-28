package doctor_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VizzleTF/owfeed/internal/apk"
	"github.com/VizzleTF/owfeed/internal/build"
	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/doctor"
	"github.com/VizzleTF/owfeed/internal/index"
	"github.com/VizzleTF/owfeed/internal/keys"
	"github.com/VizzleTF/owfeed/internal/lock"
	"github.com/VizzleTF/owfeed/internal/testapk"
)

const pkgName = "luci-app-demo"
const pkgVersion = "1.0.0-r1"

// fixture builds, signs and indexes a one-package feed, and returns the inputs a
// doctor run needs. Everything downstream deliberately breaks a real artifact
// rather than a mock: a check that only passes against a hand-written fixture has
// not been shown to catch anything.
type fixture struct {
	in       doctor.Input
	dist     string
	indexDir string
	signer   index.Signer
	trustDir string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	tool := testapk.Require(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	root := t.TempDir()
	payload := filepath.Join(root, "root")
	if err := os.MkdirAll(filepath.Join(payload, "etc", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "etc", "config", "demo"), []byte("config demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg := config.Package{
		Name: pkgName, Build: config.BuildMkpkg, Arch: "noarch",
		Version: pkgVersion, Files: "root", Conffiles: []string{"/etc/config/demo"},
	}
	cfg := &config.Config{Feed: config.Feed{Name: "demofeed"}, Packages: []config.Package{pkg}}

	dist := t.TempDir()
	if _, err := build.Build(ctx, tool, build.Request{
		Package: pkg, Root: root, Version: pkgVersion, OutDir: dist,
		SourceDateEpoch: time.Unix(1750000000, 0),
	}); err != nil {
		t.Fatalf("build: %v", err)
	}

	key, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	keyDir, trustDir := t.TempDir(), t.TempDir()
	if err := keys.WritePrivate(filepath.Join(keyDir, "feed.pem"), key); err != nil {
		t.Fatal(err)
	}
	pub, err := keys.MarshalPublic(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trustDir, "demofeed.pem"), pub, 0o644); err != nil {
		t.Fatal(err)
	}
	signer, err := index.NewSigner(keyDir, "feed.pem", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Sign(ctx, tool, dist, signer); err != nil {
		t.Fatalf("sign: %v", err)
	}

	out := t.TempDir()
	indexDir := filepath.Join(out, "releases", "25.12", "x86_64")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := build.PackageFileName(pkgName, pkgVersion)
	copyInto(t, filepath.Join(dist, name), filepath.Join(indexDir, name))
	if _, err := index.Build(ctx, tool, index.Options{Dir: indexDir, TrustDir: trustDir, Signer: signer}); err != nil {
		t.Fatalf("index: %v", err)
	}

	return fixture{
		signer:   signer,
		trustDir: trustDir,
		in: doctor.Input{
			Config:     cfg,
			Lock:       &lock.Lock{Releases: []lock.Release{{Line: "25.12", Arches: []string{"x86_64"}}}},
			Tool:       tool,
			Root:       root,
			OutDir:     out,
			Identity:   signer.Identity,
			PubKeyName: "demofeed.pem",
		},
		dist:     dist,
		indexDir: indexDir,
	}
}

func (f fixture) run(t *testing.T) *doctor.Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	r, err := doctor.Run(ctx, f.in)
	if err != nil {
		t.Fatalf("doctor.Run: %v", err)
	}
	return r
}

func TestIntegrationCleanFeedPasses(t *testing.T) {
	f := newFixture(t)
	r := f.run(t)
	if len(r.Findings) != 0 {
		t.Errorf("a feed owfeed built itself has findings:\n%s", render(r))
	}
	if r.Checked == 0 {
		t.Error("no checks ran, so passing means nothing")
	}
}

// Each case breaks a real published tree the way a real mistake would.
func TestIntegrationCatchesBreakage(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(t *testing.T, f fixture)
		wantID string
	}{
		{
			// What indexing before signing looks like from the outside: signing
			// appends bytes, so the file no longer matches its own entry.
			name: "package modified after indexing",
			break_: func(t *testing.T, f fixture) {
				path := filepath.Join(f.indexDir, build.PackageFileName(pkgName, pkgVersion))
				fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					t.Fatal(err)
				}
				defer fh.Close()
				if _, err := fh.WriteString("x"); err != nil {
					t.Fatal(err)
				}
			},
			wantID: "OWF404",
		},
		{
			// The index carries no filenames, so a missing neighbour is an entry
			// nobody can download.
			name: "indexed package missing from the directory",
			break_: func(t *testing.T, f fixture) {
				if err := os.Remove(filepath.Join(f.indexDir, build.PackageFileName(pkgName, pkgVersion))); err != nil {
					t.Fatal(err)
				}
			},
			wantID: "OWF402",
		},
		{
			// An unsigned package needs --allow-untrusted for `apk add ./file.apk`,
			// which LuCI's Upload Package flow cannot supply.
			name: "unsigned package in the tree",
			break_: func(t *testing.T, f fixture) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()

				unsigned := t.TempDir()
				if _, err := build.Build(ctx, f.in.Tool, build.Request{
					Package: f.in.Config.Packages[0], Root: f.in.Root, Version: pkgVersion,
					OutDir: unsigned, SourceDateEpoch: time.Unix(1750000000, 0),
				}); err != nil {
					t.Fatal(err)
				}
				name := build.PackageFileName(pkgName, pkgVersion)
				copyInto(t, filepath.Join(unsigned, name), filepath.Join(f.indexDir, name))
			},
			wantID: "OWF303",
		},
		{
			name: "undeclared configuration file",
			break_: func(t *testing.T, f fixture) {
				f.in.Config.Packages[0].Conffiles = nil
			},
			wantID: "OWF207",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.break_(t, f)

			r := f.run(t)
			if !hasID(r, tc.wantID) {
				t.Errorf("doctor did not report %s:\n%s", tc.wantID, render(r))
			}
			if !r.Failed(doctor.Error) {
				t.Errorf("%s did not fail the run", tc.wantID)
			}
		})
	}
}

func hasID(r *doctor.Report, id string) bool {
	for _, f := range r.Findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

func render(r *doctor.Report) string {
	var b strings.Builder
	for _, f := range r.Findings {
		b.WriteString(f.String())
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return "(no findings)"
	}
	return b.String()
}

func copyInto(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The zstd trap turns out to be structurally impossible on the path owfeed uses.
// OpenWrt builds apk with -Dzstd=disabled, and the host apk extracted from the SDK
// is built the same way, so it cannot emit an index the device could not read even
// if asked to. This is the payoff of insisting on the version-matched SDK toolchain
// rather than whatever apk is on the machine.
func TestIntegrationSDKAPKCannotProduceZstd(t *testing.T) {
	f := newFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	res, err := f.in.Tool.Run(ctx, apk.Invocation{
		Workdir: f.indexDir, KeyDir: f.signer.KeyDir, TrustDir: f.trustDir,
		Args: []string{
			"--keys-dir", apk.TrustDirRef(), "mkndx",
			"--compression", "zstd",
			"--sign-key", apk.KeyRef(f.signer.KeyName),
			"--output", "zstd.adb",
			"./" + build.PackageFileName(pkgName, pkgVersion),
		},
	})
	if err != nil {
		t.Fatalf("running mkndx: %v", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("this apk accepted --compression zstd; the resulting index would fail on every router")
	}
	if !strings.Contains(res.Stderr, "invalid argument") {
		t.Errorf("rejection came from somewhere unexpected: %s", res.Stderr)
	}
}

// 401 still earns its place for a tree that arrived from somewhere else, where the
// index was produced by an apk that does have zstd compiled in. Corrupting the magic
// is the closest reachable stand-in.
func TestIntegrationCatchesUnreadableIndex(t *testing.T) {
	f := newFixture(t)

	path := filepath.Join(f.indexDir, index.IndexFile)
	adb, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copy(adb[:4], "ADBz")
	if err := os.WriteFile(path, adb, 0o644); err != nil {
		t.Fatal(err)
	}

	r := f.run(t)
	if !hasID(r, "OWF401") {
		t.Errorf("doctor did not report OWF401 for a non-deflate index:\n%s", render(r))
	}
	if !r.Failed(doctor.Error) {
		t.Error("an unreadable index did not fail the run")
	}
}
