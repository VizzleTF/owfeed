package main

import "testing"

// The stamped value always wins. A release binary must report its tag even when
// the module system also has an opinion, because the tag is what the attestation
// and the setup action's short-circuit are matched against.
func TestStampedVersionWins(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "0.2.0"
	if got := resolveVersion(); got != "0.2.0" {
		t.Errorf("resolveVersion() = %q, want the stamped 0.2.0", got)
	}
}

// Without a stamp the module system answers, and a `go test` binary has no
// meaningful main module version -- so the honest answer is "dev" rather than a
// pseudo-version dressed up as a release number.
func TestUnstampedVersionIsNotAPseudoVersion(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = ""
	got := resolveVersion()
	if got == "" {
		t.Fatal("resolveVersion() returned an empty string")
	}
	for _, bad := range []string{"(devel)", "+dirty"} {
		if got == bad {
			t.Errorf("resolveVersion() = %q, which is not a version", got)
		}
	}
	if len(got) >= 6 && got[:6] == "0.0.0-" {
		t.Errorf("resolveVersion() = %q, a pseudo-version reported as a release", got)
	}
}
