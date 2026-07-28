package snippet_test

import (
	"strings"
	"testing"

	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/snippet"
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
		// The line nobody else emits, and the top cause of "UNTRUSTED signature"
		// reports after an upgrade.
		"/etc/sysupgrade.conf",
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
	// Listing the whole key directory in sysupgrade.conf preserves the *old* image's
	// openwrt-*.pem, which then shadows the new one.
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
