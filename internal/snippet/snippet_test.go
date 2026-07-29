package snippet_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/snippet"
)

func input() snippet.Input {
	return snippet.Input{Config: &config.Config{
		Feed:     config.Feed{Name: "demofeed", URL: "https://feed.example.org", Title: "Demo feed"},
		Layout:   config.Layout{Path: config.DefaultLayoutPath},
		Releases: []config.Release{{Line: "25.12", Default: true}},
		Packages: []config.Package{{Name: "luci-app-demo"}},
	}}
}

func TestShell(t *testing.T) {
	got := snippet.Shell(input())

	want := []string{
		// HTTPS does not work on a stock image without these.
		"apk add ca-bundle libustream-mbedtls",
		// The repository line is a direct URL to the index file: that is what apk's
		// ndx mode reads, and it appends no architecture of its own.
		"https://feed.example.org/releases/25.12/$(cat /etc/apk/arch)/packages.adb",
		"/etc/apk/repositories.d/demofeed.list",
		"/etc/apk/keys/demofeed.pem",
		// The step nobody else emits, and the top cause of "UNTRUSTED signature"
		// reports after an upgrade. keep.d rather than /etc/sysupgrade.conf: it is a
		// file of this feed's own, so re-running the install rewrites it instead of
		// appending a second copy to somebody else's config.
		"/lib/upgrade/keep.d/demofeed",
		"apk update && apk add luci-app-demo",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("snippet does not contain %q:\n%s", w, got)
		}
	}

	// The architecture comes from the file, not from `apk --print-arch`, which
	// reports the value apk was compiled with.
	if strings.Contains(got, "--print-arch") {
		t.Errorf("snippet uses apk --print-arch:\n%s", got)
	}
	// The one flag that must never appear in anything a subscriber is asked to run.
	if strings.Contains(got, "--allow-untrusted") {
		t.Errorf("snippet tells the user to bypass verification:\n%s", got)
	}
	// Listing the whole key directory preserves the *old* image's openwrt-*.pem,
	// which then shadows the new one.
	if strings.Contains(got, "/etc/apk/keys\n") || strings.Contains(got, "/etc/apk/keys ") {
		t.Errorf("snippet preserves the whole key directory rather than the feed's own key:\n%s", got)
	}
}

// The warnings are not configurable, because each describes something that only
// surprises people once they have already installed the feed.
func TestWarningsAlwaysPresent(t *testing.T) {
	md := snippet.Markdown(input())
	for _, w := range []string{
		"/etc/apk/world",   // the local-install pin
		"owut",             // attended sysupgrade will not carry the packages
		"any package name", // the trust anchor is not scoped to this feed
	} {
		if !strings.Contains(md, w) {
			t.Errorf("markdown does not warn about %q:\n%s", w, md)
		}
	}
}

func TestLayoutIsHonoured(t *testing.T) {
	in := input()
	in.Config.Layout.Path = "packages/{release}/{arch}"
	got := snippet.Shell(in)
	if !strings.Contains(got, "https://feed.example.org/packages/25.12/$(cat /etc/apk/arch)/packages.adb") {
		t.Errorf("snippet ignores the configured layout:\n%s", got)
	}
}

// A trailing slash on feed.url must not produce a doubled one: apk does not follow
// redirects, so a URL that a browser tidies up is a feed that does not resolve.
func TestTrailingSlashIsNotDoubled(t *testing.T) {
	in := input()
	in.Config.Feed.URL = "https://feed.example.org/"
	if got := snippet.Shell(in); strings.Contains(got, "org//") {
		t.Errorf("snippet contains a doubled slash:\n%s", got)
	}
}

// bothLines is a feed serving apk and opkg, which is what the subscribe script
// exists for: one file that works whichever of the two the router runs.
func bothLines() snippet.Input {
	in := input()
	in.Config.Releases = []config.Release{
		{Line: "25.12", Default: true, Format: config.FormatAPK},
		{Line: "24.10", Format: config.FormatIPK},
	}
	in.UsignKeyID = "deadbeefdeadbeef"
	return in
}

func TestScriptCoversBothManagers(t *testing.T) {
	got := snippet.Script(bothLines())

	want := []string{
		// The whole point: the router is asked, rather than the reader.
		"if command -v apk >/dev/null 2>&1; then",
		// Both lines' URLs are built from $line, so a feed adding a release line
		// cannot leave the script advertising only the old one.
		"https://feed.example.org/releases/$line/$arch/packages.adb",
		"src/gz demofeed https://feed.example.org/releases/$line/$arch",
		// opkg finds a key by filename, and the filename is the id.
		"/etc/opkg/keys/deadbeefdeadbeef",
		"/etc/apk/keys/demofeed.pem",
		// Both managers lose their key across sysupgrade without this.
		"/lib/upgrade/keep.d/demofeed",
		// Re-running must not append a second feed line.
		"sed -i \"/^src\\\\/gz demofeed /d\" /etc/opkg/customfeeds.conf",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("script does not contain %q:\n%s", w, got)
		}
	}
}

// A release line the feed does not publish for has to stop the script. Guessing
// produces a URL that 404s at `apk update`, which reads as a broken feed and
// sends the report to the wrong person.
func TestScriptNamesTheLinesItServes(t *testing.T) {
	got := snippet.Script(bothLines())

	for _, w := range []string{
		`case " 25.12 " in *" $line "*) ;; *) unsupported "25.12" apk ;; esac`,
		`case " 24.10 " in *" $line "*) ;; *) unsupported "24.10" opkg ;; esac`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("script does not contain %q:\n%s", w, got)
		}
	}
}

// An apk-only feed has no opkg branch to offer, and saying so beats fetching a
// key file that was never published.
func TestScriptRefusesOpkgWhenNoIPKLine(t *testing.T) {
	got := snippet.Script(input())

	if strings.Contains(got, "opkg update") {
		t.Errorf("apk-only feed emitted an opkg branch:\n%s", got)
	}
	if !strings.Contains(got, "publishes nothing for opkg") {
		t.Errorf("apk-only feed does not say why opkg is unsupported:\n%s", got)
	}
}

// The script is the one output of this package that is executed rather than
// read, and a syntax error in it reaches routers as a copy-paste that does
// nothing. `sh -n` parses without running.
func TestScriptParses(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this machine")
	}
	for name, in := range map[string]snippet.Input{"both": bothLines(), "apk-only": input()} {
		path := filepath.Join(t.TempDir(), "subscribe.sh")
		if err := os.WriteFile(path, []byte(snippet.Script(in)), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(sh, "-n", path).CombinedOutput()
		if err != nil {
			t.Errorf("%s: sh -n: %v\n%s\n%s", name, err, out, snippet.Script(in))
		}
	}
}
