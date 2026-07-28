package smoke

import (
	"strings"
	"testing"
)

// A dual-line feed must send each router the script its package manager can run.
// Reading the format off the default line instead of the requested one sent the apk
// script to a 24.10 router, where the first command is `apk` and there is no apk.
func TestScriptMatchesTheRequestedFormat(t *testing.T) {
	pkgs := []string{"luci-app-demo"}

	apk := script("myfeed", "25.12", "releases/{release}/{arch}", "x86_64", pkgs)
	if !strings.Contains(apk, "apk update") || strings.Contains(apk, "opkg") {
		t.Errorf("the apk script is not an apk script:\n%s", apk)
	}
	if !strings.Contains(apk, "/etc/apk/repositories.d/myfeed.list") {
		t.Error("the apk repository line does not name the index file")
	}

	opkg := scriptOpkg("myfeed", "24.10", "releases/{release}/{arch}", "x86_64", "0bdde418579c2850", pkgs)
	if !strings.Contains(opkg, "opkg update") || strings.Contains(opkg, "apk ") {
		t.Errorf("the opkg script is not an opkg script:\n%s", opkg)
	}
	// The key's filename is its id: opkg looks it up by that, where apk matches on
	// the identity inside the signature and ignores the name.
	if !strings.Contains(opkg, "/etc/opkg/keys/0bdde418579c2850") {
		t.Error("the opkg script does not install the key under its id")
	}
	// opkg is pointed at the directory and appends the filename itself.
	if strings.Contains(opkg, "Packages.gz\"") {
		t.Error("the opkg repository line names a file rather than the directory")
	}
	// Nothing a smoke run executes may turn verification off. The apk script names
	// the flag in a comment explaining why it is not needed, which is the point of
	// looking at commands rather than at the whole text.
	for _, s := range []string{apk, opkg} {
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(line, "allow-untrusted") {
				t.Errorf("a smoke script disables verification: %s", line)
			}
		}
	}
}
