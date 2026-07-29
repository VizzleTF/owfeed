package meta_test

import (
	"strings"
	"testing"

	"owfeed.org/owfeed/internal/meta"
)

// versionCorpus is shared with the integration test, which puts every entry to the
// real apk. Anything added here is therefore also a claim about apk's behaviour, and
// the integration test will say so if the claim is wrong.
var versionCorpus = []struct {
	v     string
	valid bool
	// why documents what the case is actually about, for entries whose verdict is
	// not obvious from the string.
	why string
}{
	{v: "1.0", valid: true},
	{v: "0", valid: true},
	{v: "1.2.3-r1", valid: true},
	{v: "20260213-r0", valid: true, why: "LuCI packages version by date"},
	{v: "1.2.3a-r0", valid: true, why: "a single trailing letter is allowed"},
	{v: "1.0_pre1", valid: true},
	{v: "1.0_beta2-r3", valid: true},
	{v: "1.0_alpha_pre1", valid: true, why: "suffixes stack"},
	{v: "1.0_git20260101-r1", valid: true, why: "the shape OpenWrt uses for snapshots"},
	{v: "1.0~abc123-r1", valid: true, why: "~ introduces a commit hash"},
	{v: "1.0_pre1~ff-r1", valid: true},

	{v: "v1.0", valid: false, why: "a git tag is not a version"},
	{v: "1.0~beta", valid: false, why: "the single most likely mistake: t is not a hex digit"},
	{v: "1.0~ABC", valid: false, why: "apk's hex class is lowercase only"},
	{v: "1.0~", valid: false},
	{v: "1.0-beta", valid: false, why: "a dash may only introduce -r"},
	{v: "1.0_dev", valid: false, why: "_dev is not in apk's suffix set"},
	{v: "1.0_final", valid: false},
	{v: "1.0-r", valid: false},
	{v: "1.0-r1-r2", valid: false},
	{v: "1.0a1", valid: false, why: "nothing may follow the trailing letter"},
	{v: "1.0.", valid: false},
	{v: "1..2", valid: false},
	{v: "1.0-1", valid: false},
	{v: ".1", valid: false},
	{v: "1.0+build5", valid: false, why: "semver build metadata has no place in an apk version"},
}

func TestValidateVersion(t *testing.T) {
	for _, tc := range versionCorpus {
		err := meta.ValidateVersion(tc.v)
		switch {
		case tc.valid && err != nil:
			t.Errorf("ValidateVersion(%q) = %v, want accepted (%s)", tc.v, err, tc.why)
		case !tc.valid && err == nil:
			t.Errorf("ValidateVersion(%q) accepted it, want rejected (%s)", tc.v, tc.why)
		}
	}

	if err := meta.ValidateVersion(""); err == nil {
		t.Error("ValidateVersion(\"\") accepted an empty version")
	}
}

// The error for 1.0~beta is the one users will actually meet, since ~ is how every
// other packaging system spells a pre-release. It has to name the way out, not just
// the rule.
func TestTildeErrorSuggestsPreRelease(t *testing.T) {
	err := meta.ValidateVersion("1.0~beta")
	if err == nil {
		t.Fatal("1.0~beta was accepted")
	}
	msg := err.Error()
	for _, want := range []string{"hex", "_pre1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not mention %q:\n%s", want, msg)
		}
	}
}

func TestVersionAdvice(t *testing.T) {
	if got := meta.VersionAdvice("1.2.3-r1"); len(got) != 0 {
		t.Errorf("VersionAdvice(1.2.3-r1) = %v, want nothing to say", got)
	}
	if got := meta.VersionAdvice("1.2.3"); len(got) != 1 || !strings.Contains(got[0], "-r") {
		t.Errorf("VersionAdvice(1.2.3) = %v, want a note about the missing revision", got)
	}
	if got := meta.VersionAdvice("1.0~abc-r1"); len(got) != 1 || !strings.Contains(got[0], "17237") {
		t.Errorf("VersionAdvice(1.0~abc-r1) = %v, want the openwrt#17237 note", got)
	}
}

func TestValidateInfo(t *testing.T) {
	if err := meta.ValidateInfo("description", "A theme, with \"quotes\", $vars and `ticks`."); err != nil {
		// owfeed execs apk directly, so shell metacharacters are ordinary text here.
		// Escaping them the way package-pack.mk does would put literal backslashes in
		// the published description.
		t.Errorf("ValidateInfo rejected shell metacharacters: %v", err)
	}
	if err := meta.ValidateInfo("description", "line one\nline two"); err == nil {
		t.Error("ValidateInfo accepted a newline in a value")
	}
	if err := meta.ValidateInfo("installed-size", "1234"); err == nil {
		t.Error("ValidateInfo accepted installed-size, which apk refuses on the command line")
	}
	if err := meta.ValidateInfo("hashes", "deadbeef"); err == nil {
		t.Error("ValidateInfo accepted hashes, which apk overwrites while packing")
	}
	if err := meta.ValidateInfo("summary", "x"); err == nil {
		t.Error("ValidateInfo accepted a field apk does not have")
	}
	if err := meta.ValidateInfo("tags", "openwrt:abiversion=5"); err != nil {
		t.Errorf("ValidateInfo rejected the ABI tag: %v", err)
	}
}

func TestABISuffix(t *testing.T) {
	tests := []struct{ name, abi, want string }{
		{"libjson-c", "5", "5"},
		{"libblobmsg-json", "20260213", "20260213"},
		{"libfoo2", "3", "-3"},
		{"libfoo", "", ""},
	}
	for _, tc := range tests {
		if got := meta.ABISuffix(tc.name, tc.abi); got != tc.want {
			t.Errorf("ABISuffix(%q, %q) = %q, want %q", tc.name, tc.abi, got, tc.want)
		}
	}
}
