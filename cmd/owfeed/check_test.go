package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const checkConfig = `version: 1
feed:
  name: demo
  url: https://github.com/someone/demo
  maintainer: "Someone <someone@example.org>"
publish:
  - target: github-pages
packages:
  - name: demo
    build: mkpkg
    arch: noarch
    version: 1.0.0-r1
    files: ./root
    description: "A demo."
    url: https://github.com/someone/demo
`

// The whole point of `check`: a config that declares NO signing keys still gets
// built, signed and indexed. Every repository that publishes release assets rather
// than a feed was declaring a `signing:` block it had no use for, because `index`
// refused to run without a usign key.
func TestCheckNeedsNoDeclaredKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "root", "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "owfeed.yml"), checkConfig)

	a := &app{configPath: filepath.Join(root, "owfeed.yml"), out: &bytes.Buffer{}, err: &bytes.Buffer{}}

	c, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Signing.UsignKey != "" {
		t.Fatalf("the fixture declares a usign key: %q", c.Signing.UsignKey)
	}

	cleanup, err := a.ephemeralKeys()
	if err != nil {
		t.Fatal(err)
	}

	c, err = a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Signing.Key, "env:") || !strings.HasPrefix(c.Signing.UsignKey, "env:") {
		t.Fatalf("keys are %q and %q, want both from the environment", c.Signing.Key, c.Signing.UsignKey)
	}
	if c.Signing.SignPackages == nil || !*c.Signing.SignPackages {
		t.Error("check must sign the packages: a build that cannot be signed is what it is looking for")
	}
	// The key material is reachable where the config says it is, or every stage
	// downstream fails on a variable that is not set.
	for _, spec := range []string{c.Signing.Key, c.Signing.UsignKey} {
		if os.Getenv(strings.TrimPrefix(spec, "env:")) == "" {
			t.Errorf("%s names an empty variable", spec)
		}
	}

	// Nothing outlives the run. A key left in the environment is one a later stage
	// could sign something real with.
	ecVar := strings.TrimPrefix(c.Signing.Key, "env:")
	usignVar := strings.TrimPrefix(c.Signing.UsignKey, "env:")
	cleanup()
	if os.Getenv(ecVar) != "" || os.Getenv(usignVar) != "" {
		t.Error("a throwaway key survived the run")
	}
	if a.checkKeys != nil {
		t.Error("the override survived the run")
	}
	if c, err := a.loadConfig(); err != nil {
		t.Fatal(err)
	} else if c.Signing.UsignKey != "" {
		t.Errorf("the config kept an injected key: %q", c.Signing.UsignKey)
	}
}

// --require-origin defaults ON here where `doctor` defaults it off. A feed may
// reasonably carry a package that names no upstream; an author checking their own
// release before a tag has no excuse, and the community feeds refuse it at ingest.
func TestCheckRequiresOriginByDefault(t *testing.T) {
	got := strings.Join(doctorArgs(true, "error"), " ")
	if !strings.Contains(got, "--require-origin") {
		t.Errorf("doctor args = %q, want --require-origin", got)
	}
	if got := strings.Join(doctorArgs(false, "warn"), " "); strings.Contains(got, "--require-origin") {
		t.Errorf("doctor args = %q, want it opted out", got)
	} else if !strings.Contains(got, "--fail-on warn") {
		t.Errorf("doctor args = %q, want --fail-on warn", got)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
