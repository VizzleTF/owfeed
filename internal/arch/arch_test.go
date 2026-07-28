package arch

import (
	"strings"
	"testing"
)

// listing is trimmed from the real nginx autoindex at
// https://downloads.openwrt.org/releases/25.12.5/packages/ — enough entries to pass
// the plausibility floor, and the shapes that matter.
const listing = `<html>
<head><title>Index of /releases/25.12.5/packages/</title></head>
<body>
<h1>Index of /releases/25.12.5/packages/</h1><hr><pre><a href="../">../</a>
<a href="aarch64_cortex-a53/">aarch64_cortex-a53/</a>                                 30-Jun-2026 16:44                   -
<a href="aarch64_generic/">aarch64_generic/</a>                                       30-Jun-2026 16:44                   -
<a href="arm_arm1176jzf-s_vfp/">arm_arm1176jzf-s_vfp/</a>                             30-Jun-2026 16:44                   -
<a href="arm_cortex-a7_neon-vfpv4/">arm_cortex-a7_neon-vfpv4/</a>                     30-Jun-2026 16:44                   -
<a href="i386_pentium-mmx/">i386_pentium-mmx/</a>                                     30-Jun-2026 16:44                   -
<a href="loongarch64_generic/">loongarch64_generic/</a>                               30-Jun-2026 16:44                   -
<a href="mips64el_mips64r2/">mips64el_mips64r2/</a>                                   30-Jun-2026 16:44                   -
<a href="mipsel_24kc_24kf/">mipsel_24kc_24kf/</a>                                     30-Jun-2026 16:44                   -
<a href="powerpc_8548/">powerpc_8548/</a>                                             30-Jun-2026 16:44                   -
<a href="riscv64_generic/">riscv64_generic/</a>                                       30-Jun-2026 16:44                   -
<a href="x86_64/">x86_64/</a>                                                         30-Jun-2026 16:44                   -
<a href="index.html">index.html</a>                                                  30-Jun-2026 16:44                 512
</pre><hr></body>
</html>`

func TestParseListing(t *testing.T) {
	got, err := parseListing(listing)
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}

	// The parent link and plain files are not architectures.
	for _, unwanted := range []string{"..", "index.html"} {
		for _, a := range got {
			if a == unwanted {
				t.Errorf("derived %q as an architecture", unwanted)
			}
		}
	}
	// These are the names the ecosystem's hand-written matrices get wrong: the
	// renames are simply what upstream publishes now.
	for _, want := range []string{"riscv64_generic", "powerpc_8548", "x86_64", "arm_arm1176jzf-s_vfp"} {
		if !contains(got, want) {
			t.Errorf("derived set is missing %s: %v", want, got)
		}
	}
	if !sorted(got) {
		t.Errorf("derived set is not sorted: %v", got)
	}
}

// A page that stops being a listing must not derive a short set and report success:
// that would publish a feed covering almost nothing while looking like it worked.
func TestParseListingRejectsUnrecognisedPage(t *testing.T) {
	_, err := parseListing(`<html><body><a href="one/">one/</a><a href="two/">two/</a></body></html>`)
	if err == nil {
		t.Fatal("parseListing accepted a two-entry page")
	}
	if !strings.Contains(err.Error(), "format has probably changed") {
		t.Errorf("error does not point at the real cause: %v", err)
	}
}

func TestPickLatest(t *testing.T) {
	list := []string{
		"25.12.5", "25.12.4", "25.12.10", "25.12.0-rc5", "25.12.0",
		"24.10.8", "24.10.0-rc7", "23.05.5",
	}
	tests := []struct{ line, want string }{
		// Ten is greater than five: the list is compared numerically, not as text.
		{"25.12", "25.12.10"},
		{"24.10", "24.10.8"},
		{"23.05", "23.05.5"},
	}
	for _, tc := range tests {
		got, err := pickLatest(tc.line, list)
		if err != nil {
			t.Fatalf("pickLatest(%q): %v", tc.line, err)
		}
		if got != tc.want {
			t.Errorf("pickLatest(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}

	// A release candidate is not a release: pinning to one pins to something that
	// will stop existing.
	if got, err := pickLatest("26.04", []string{"26.04.0-rc1", "26.04.0-rc2"}); err == nil {
		t.Errorf("pickLatest returned %q for a line with only release candidates", got)
	}
	if _, err := pickLatest("99.99", list); err == nil {
		t.Error("pickLatest accepted a line that does not exist")
	}
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func sorted(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}
