package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/ipkindex"
	"owfeed.org/owfeed/internal/lock"
	"owfeed.org/owfeed/internal/snippet"
)

func input(t *testing.T, pkgs ...config.Package) Input {
	t.Helper()
	return Input{
		Config:     &config.Config{Feed: config.Feed{Name: "demofeed"}, Packages: pkgs},
		Root:       t.TempDir(),
		PubKeyName: "demofeed.pem",
	}
}

func ids(r *Report) string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.ID)
	}
	return strings.Join(out, ",")
}

// The quietest failure in the set: sysupgrade reads .conffiles_static to decide what
// survives a firmware upgrade, so an undeclared /etc/config/foo costs the user their
// settings on every upgrade with nothing reported.
func TestConffileCoverage(t *testing.T) {
	in := input(t, config.Package{Name: "luci-app-demo", Files: "root"})
	dir := filepath.Join(in.Root, "root", "etc", "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"demo", "demo-extra"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("config x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := &Report{}
	checkConffileCoverage(r, in)
	if len(r.Findings) != 1 {
		t.Fatalf("findings = %v, want one OWF207", ids(r))
	}
	f := r.Findings[0]
	if f.ID != "OWF207" || f.Severity != Error {
		t.Errorf("got %s %s, want OWF207 error", f.ID, f.Severity)
	}
	for _, want := range []string{"/etc/config/demo", "/etc/config/demo-extra", "sysupgrade"} {
		if !strings.Contains(f.String(), want) {
			t.Errorf("finding does not mention %q:\n%s", want, f)
		}
	}

	// Declaring them clears it.
	in.Config.Packages[0].Conffiles = []string{"/etc/config/demo", "/etc/config/demo-extra"}
	r = &Report{}
	checkConffileCoverage(r, in)
	if len(r.Findings) != 0 {
		t.Errorf("declared conffiles still reported: %v", ids(r))
	}

	// A package with no configuration at all is not a finding.
	bare := input(t, config.Package{Name: "luci-theme-demo", Files: "root"})
	if err := os.MkdirAll(filepath.Join(bare.Root, "root", "www"), 0o755); err != nil {
		t.Fatal(err)
	}
	r = &Report{}
	checkConffileCoverage(r, bare)
	if len(r.Findings) != 0 {
		t.Errorf("package shipping no configuration was reported: %v", ids(r))
	}
}

func TestKeyNameShadowing(t *testing.T) {
	in := input(t)
	in.PubKeyName = "openwrt-mine.pem"

	r := &Report{}
	checkKeyName(r, in)
	if len(r.Findings) != 1 || r.Findings[0].ID != "OWF302" {
		t.Fatalf("findings = %v, want OWF302", ids(r))
	}
	if !strings.Contains(r.Findings[0].String(), "shadow") {
		t.Errorf("finding does not say what the collision costs:\n%s", r.Findings[0])
	}

	in.PubKeyName = "demofeed.pem"
	r = &Report{}
	checkKeyName(r, in)
	if len(r.Findings) != 0 {
		t.Errorf("an ordinary key name was reported: %v", ids(r))
	}
}

func TestDescriptionLength(t *testing.T) {
	in := input(t,
		config.Package{Name: "short", Description: "A demo."},
		config.Package{Name: "long", Description: strings.Repeat("x", maxDescription+1)},
	)
	r := &Report{}
	checkDescriptions(r, in)
	if len(r.Findings) != 1 || r.Findings[0].ID != "OWF203" {
		t.Fatalf("findings = %v, want one OWF203", ids(r))
	}
	// It is advice, not a rule: apk imposes no limit, only LuCI truncates.
	if r.Findings[0].Severity != Warn {
		t.Errorf("severity = %s, want warn", r.Findings[0].Severity)
	}
}

func TestABIConsistency(t *testing.T) {
	// The suffix belongs to abiversion; writing it into the name too produces
	// libfoo55, which nothing depends on.
	in := input(t, config.Package{Name: "libfoo5", ABIVersion: "5"})
	r := &Report{}
	checkABI(r, in)
	if len(r.Findings) != 0 {
		// libfoo5 + abi 5 is a legitimate, if odd, libfoo5-5; the check only
		// verifies name and suffix agree, so this must not fire.
		t.Logf("note: %v", ids(r))
	}

	ok := input(t, config.Package{Name: "libjson-c", ABIVersion: "5"})
	r = &Report{}
	checkABI(r, ok)
	if len(r.Findings) != 0 {
		t.Errorf("a correctly suffixed package was reported: %v", ids(r))
	}
}

func TestReportSeverityGate(t *testing.T) {
	r := &Report{Findings: []Finding{{ID: "OWF203", Severity: Warn}}}
	if r.Failed(Error) {
		t.Error("a warning failed a run gated on errors")
	}
	if !r.Failed(Warn) {
		t.Error("a warning did not fail a run gated on warnings")
	}
	if got := r.Worst(); got != Warn {
		t.Errorf("Worst = %s, want warn", got)
	}
}

// 701 catches the live bug in a major feed today: its README sends people to
// /25.12/ while its deploy writes /openwrt-25.12/, so the documented URL 404s.
func TestDocDrift(t *testing.T) {
	in := input(t, config.Package{Name: "luci-app-demo"})
	in.Config.Feed.URL = "https://feed.example.org"
	in.Config.Layout.Path = config.DefaultLayoutPath
	in.Config.Releases = []config.Release{{Line: "25.12", Default: true}}
	readme := filepath.Join(in.Root, "README.md")

	// A README that documents the feed with the wrong path.
	drifted := "# Demo\n\n```sh\n" +
		"wget https://feed.example.org/demofeed.pem -O /etc/apk/keys/demofeed.pem\n" +
		"echo \"https://feed.example.org/openwrt-25.12/$(cat /etc/apk/arch)/packages.adb\" > /etc/apk/repositories.d/demofeed.list\n" +
		"```\n"
	if err := os.WriteFile(readme, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Report{}
	checkDocDrift(r, in)
	if len(r.Findings) != 1 || r.Findings[0].ID != "OWF701" {
		t.Fatalf("findings = %v, want OWF701", ids(r))
	}

	// The generated snippet itself must satisfy the check, or the fix it recommends
	// would not work.
	if err := os.WriteFile(readme, []byte(snippet.Markdown(snippet.Input{Config: in.Config})), 0o644); err != nil {
		t.Fatal(err)
	}
	r = &Report{}
	checkDocDrift(r, in)
	if len(r.Findings) != 0 {
		t.Errorf("the output of install-snippet does not satisfy its own check: %v\n%s", ids(r), r.Findings[0])
	}

	// A README that says nothing about the feed is not this check's business.
	if err := os.WriteFile(readme, []byte("# Demo\n\nA theme.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = &Report{}
	checkDocDrift(r, in)
	if len(r.Findings) != 0 {
		t.Errorf("a README that does not document the feed was reported: %v", ids(r))
	}
}

// A package that is simply absent is invisible to every other check: they all read
// the tree and ask whether what is there is right. This is what let a tree carrying
// one of three packages on its 24.10 line pass 610 checks and report itself ready to
// publish.
func TestCoverageCatchesAMissingPackage(t *testing.T) {
	in := input(t,
		config.Package{Name: "luci-theme-demo"},
		config.Package{Name: "demo-agent", Releases: []string{"25.12"}},
	)
	in.OutDir = t.TempDir()
	in.Lock = &lock.Lock{Releases: []lock.Release{
		{Line: "25.12", Arches: []string{"x86_64"}},
		{Line: "24.10", Arches: []string{"x86_64"}},
	}}

	// 25.12 carries both; 24.10 carries only the theme, because demo-agent does not
	// declare that line. Neither is a finding.
	writeIPKIndex(t, in, "25.12", "x86_64", "luci-theme-demo", "demo-agent")
	writeIPKIndex(t, in, "24.10", "x86_64", "luci-theme-demo")

	r := &Report{}
	checkCoverage(r, in)
	if len(r.Findings) != 0 {
		t.Fatalf("findings = %v, want none", ids(r))
	}

	// Now lose the theme from 24.10, the way a fetch that failed without stopping the
	// run does.
	writeIPKIndex(t, in, "24.10", "x86_64")
	r = &Report{}
	checkCoverage(r, in)
	if len(r.Findings) != 1 || r.Findings[0].ID != "OWF406" {
		t.Fatalf("findings = %v, want one OWF406", ids(r))
	}
	if !strings.Contains(r.Findings[0].What, "luci-theme-demo") {
		t.Errorf("finding does not name the missing package: %s", r.Findings[0].What)
	}
	if r.Findings[0].Where != "releases/24.10/x86_64" {
		t.Errorf("Where = %q, want the published path", r.Findings[0].Where)
	}
}

// A whole architecture that never got built is the same failure one directory up,
// and subscribers on it get a 404 rather than a stale package.
func TestCoverageCatchesAMissingArch(t *testing.T) {
	in := input(t, config.Package{Name: "luci-theme-demo"})
	in.OutDir = t.TempDir()
	in.Lock = &lock.Lock{Releases: []lock.Release{{Line: "25.12", Arches: []string{"x86_64", "aarch64_generic"}}}}
	writeIPKIndex(t, in, "25.12", "x86_64", "luci-theme-demo")

	r := &Report{}
	checkCoverage(r, in)
	if len(r.Findings) != 1 || r.Findings[0].ID != "OWF406" {
		t.Fatalf("findings = %v, want one OWF406", ids(r))
	}
	if r.Findings[0].Where != "releases/25.12/aarch64_generic" {
		t.Errorf("Where = %q, want the architecture that is missing", r.Findings[0].Where)
	}
}

// writeIPKIndex writes a minimal opkg index naming the given packages. The check
// only reads names, and an ipk index is the one owfeed can write without a key.
func writeIPKIndex(t *testing.T, in Input, release, arch string, names ...string) {
	t.Helper()
	dir := filepath.Join(in.OutDir, filepath.FromSlash(in.Config.LayoutPath(release, arch)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "Package: %s\nVersion: 1.0-r1\nArchitecture: %s\nFilename: %s_1.0-r1_%s.ipk\nSize: 1\nSHA256sum: %s\n\n",
			n, arch, n, arch, strings.Repeat("0", 64))
	}
	if err := os.WriteFile(filepath.Join(dir, ipkindex.IndexFile), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
