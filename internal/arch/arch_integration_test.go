package arch_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"owfeed.org/owfeed/internal/arch"
)

func requireNetwork(t *testing.T) {
	t.Helper()
	if os.Getenv("OWFEED_INTEGRATION") == "" {
		t.Skip("set OWFEED_INTEGRATION=1 to run (talks to downloads.openwrt.org)")
	}
}

// Deriving from the live server is the only way to know the parser still matches
// what OpenWrt serves. A fixture cannot notice the page changing.
func TestIntegrationDerive(t *testing.T) {
	requireNetwork(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	cache := t.TempDir()
	res, err := arch.Derive(ctx, http.DefaultClient, cache, "25.12.5")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(res.Arches) < 30 {
		t.Errorf("derived %d architectures, expected upstream to publish far more: %v", len(res.Arches), res.Arches)
	}
	for _, want := range []string{"x86_64", "aarch64_cortex-a53", "riscv64_generic"} {
		if !hasArch(res.Arches, want) {
			t.Errorf("derived set is missing %s: %v", want, res.Arches)
		}
	}
	// The names the ecosystem's hardcoded matrices still carry from older releases.
	for _, gone := range []string{"riscv64_riscv64", "powerpc_8540", "mips_4kec"} {
		if hasArch(res.Arches, gone) {
			t.Errorf("derived set contains %s, which 25.12 does not publish", gone)
		}
	}
	if res.FromCache {
		t.Error("a live derivation reported itself as cached")
	}

	// Deriving saves the set so an offline run has something to work from. It is a
	// separate call, never a silent fallback: which of the two a feed was published
	// from must not depend on whether the network happened to be reachable.
	offline, err := arch.Cached(cache, "25.12.5")
	if err != nil {
		t.Fatalf("Cached after Derive: %v", err)
	}
	if !offline.FromCache {
		t.Error("Cached did not report itself as cached")
	}
	if len(offline.Arches) != len(res.Arches) {
		t.Errorf("cached set has %d entries, fresh had %d", len(offline.Arches), len(res.Arches))
	}
	if _, err := arch.Cached(t.TempDir(), "25.12.5"); err == nil {
		t.Error("Cached invented an architecture set for an empty cache")
	}
}

func TestIntegrationLatestPoint(t *testing.T) {
	requireNetwork(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	got, err := arch.LatestPoint(ctx, http.DefaultClient, "25.12")
	if err != nil {
		t.Fatalf("LatestPoint: %v", err)
	}
	// Asserting the exact value would fail the day OpenWrt ships a point release,
	// which is not a bug in owfeed. Assert the shape instead.
	if len(got) < len("25.12.0") || got[:6] != "25.12." {
		t.Errorf("LatestPoint(25.12) = %q, want a 25.12.x point release", got)
	}
	if _, err := arch.Derive(ctx, http.DefaultClient, t.TempDir(), got); err != nil {
		t.Errorf("the resolved point release %q has no package directory: %v", got, err)
	}
}

func hasArch(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
