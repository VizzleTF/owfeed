package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/meta"
	"github.com/VizzleTF/owfeed/internal/snippet"
)

func (a *app) init_(args []string) error {
	fs := flag.NewFlagSet("owfeed init", flag.ContinueOnError)
	fs.SetOutput(a.err)
	name := fs.String("name", "", "feed name (default: the directory name)")
	url := fs.String("url", "", "the URL the feed will be served from")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	if _, err := os.Stat(a.configPath); err == nil {
		return fail(exitConfig, "%s already exists", a.configPath)
	}

	feedName := *name
	if feedName == "" {
		wd, err := os.Getwd()
		if err != nil {
			return wrap(exitInternal, err)
		}
		feedName = sanitiseName(filepath.Base(wd))
	}
	feedURL := *url
	if feedURL == "" {
		feedURL = "https://feed.example.org"
	}

	if err := os.WriteFile(a.configPath, []byte(scaffold(feedName, feedURL)), 0o644); err != nil {
		return wrap(exitConfig, err)
	}
	a.logf("wrote %s", a.configPath)

	if err := a.ensureGitignore(); err != nil {
		return err
	}

	a.logf("")
	// This is said once, at the moment the decision is actually being made.
	a.logf("Before you publish a feed, read this once:")
	a.logf("")
	a.logf("  Asking people to install your key in /etc/apk/keys asks them to trust it for")
	a.logf("  every package name, not only yours. A feed whose key is compromised can offer a")
	a.logf("  higher version of dropbear or base-files and win the resolution. apk has no")
	a.logf("  revocation — no CRL, no expiry, no way to say a key is dead — so there is no")
	a.logf("  recovery path for a device that is offline when it matters.")
	a.logf("")
	a.logf("  If you ship one package that people install occasionally, signed release")
	a.logf("  artifacts are a smaller ask. A feed earns its keep when you ship several")
	a.logf("  packages, or when you want `apk upgrade` to work.")
	a.logf("")
	a.logf("Next: owfeed keygen, then edit %s and run owfeed lock --update.", a.configPath)
	return nil
}

// ensureGitignore adds the key patterns, and says so. A signing key committed by
// accident cannot be un-published, and apk offers no way to revoke it.
func (a *app) ensureGitignore() error {
	path := filepath.Join(a.root(), ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return wrap(exitConfig, err)
	}

	want := []string{"*.pem", "*.key"}
	var missing []string
	lines := strings.Split(string(existing), "\n")
	for _, w := range want {
		found := false
		for _, l := range lines {
			if strings.TrimSpace(l) == w {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, w)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\n# owfeed: a published signing key cannot be revoked.\n")
	for _, m := range missing {
		b.WriteString(m + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return wrap(exitConfig, err)
	}
	a.logf("added %s to .gitignore", strings.Join(missing, " and "))
	return nil
}

func sanitiseName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "myfeed"
	}
	return out
}

func scaffold(name, url string) string {
	// No $schema modeline. There was one, pointing at a host that does not exist and
	// a file that was never published — so the first line of every config owfeed
	// generated was a promise it did not keep. Until then the validator is the
	// specification, and it rejects an unknown key rather than warning about it.
	//
	// The schema now exists: internal/schema generates it from internal/config, and
	// .github/workflows/pages.yml publishes it. The modeline comes back when the URL
	// in schema.ID actually answers — not when the file exists in the repository.
	// That is the same mistake as last time, one directory further along. Check it,
	// then add:
	//
	//	# yaml-language-server: $schema=https://owfeed.org/schema/v1.json
	//
	//	$ curl -fsS https://owfeed.org/schema/v1.json | head -1
	return fmt.Sprintf(`# Reference: https://github.com/VizzleTF/owfeed/blob/main/docs/examples.md
version: 1

feed:
  name: %s
  # The address apk will actually fetch. apk does not follow redirects with the
  # stock uclient-fetch, so an apex that redirects to www, or http that upgrades to
  # https, is a broken feed even though it works in a browser.
  url: %s
  title: %s
  maintainer: "Your Name <you@example.org>"

publish:
  - target: github-pages

packages:
  # apk mkpkg packages a directory that is already laid out the way it should be
  # installed. For a LuCI package that means the CSS is built and the .po files are
  # already compiled to .lmo catalogues.
  - name: luci-app-example
    build: mkpkg
    arch: noarch          # never "all": apk rejects it as uninstallable
    version: 1.0.0-r1     # or version-from: makefile:./path/to/Makefile
    files: ./dist/root
    description: "One line. LuCI truncates past %d bytes."
    depends: [luci-base]
    # Every /etc/config file the package ships must be listed here, or sysupgrade
    # replaces the user's settings with the package defaults on every upgrade.
    conffiles: []
`, name, url, name, meta.MaxDescriptionBytes)
}

func (a *app) installSnippet(args []string) error {
	fs := flag.NewFlagSet("owfeed install-snippet", flag.ContinueOnError)
	fs.SetOutput(a.err)
	format := fs.String("format", "md", "md or sh")
	pkg := fs.String("package", "", "the package used in the example")
	release := fs.String("release", "", "release line (default: the one owfeed.yml advertises)")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	c, err := a.loadConfig()
	if err != nil {
		return err
	}
	in := snippet.Input{Config: c, Package: *pkg, Release: *release}
	line := *release
	if line == "" {
		line = c.DefaultRelease().Line
	}
	// An ipk line's snippet names the key by its id, because that is the filename
	// opkg looks it up under.
	if in.Format(line) == config.FormatIPK {
		key, err := a.usignKey(c)
		if err != nil {
			return err
		}
		in.UsignKeyID = key.ID.String()
	}

	switch *format {
	case "md":
		fmt.Fprint(a.out, snippet.Markdown(in))
	case "sh":
		fmt.Fprint(a.out, snippet.Shell(in))
	default:
		return fail(exitConfig, "--format %q is neither md nor sh", *format)
	}
	return nil
}
