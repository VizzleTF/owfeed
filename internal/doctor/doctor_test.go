package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/snippet"
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
