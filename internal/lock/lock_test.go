package lock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() *Lock {
	return &Lock{
		Version: Version,
		Releases: []Release{{
			Line:   "25.12",
			Point:  "25.12.5",
			Source: "https://downloads.openwrt.org/releases/25.12.5/packages/",
			Arches: []string{"x86_64", "aarch64_cortex-a53", "riscv64_generic"},
		}},
		Toolchain: Toolchain{SDKRelease: "25.12.5", APKTools: "apk-tools 3.0.5"},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), Name)
	if err := Save(path, sample()); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Commit this file") {
		t.Error("lockfile carries no explanation of what it is")
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r, ok := got.Release("25.12")
	if !ok {
		t.Fatal("round trip lost the release line")
	}
	// Save sorts, so two runs over the same facts produce the same bytes and a diff
	// only ever shows a real change.
	want := "aarch64_cortex-a53,riscv64_generic,x86_64"
	if strings.Join(r.Arches, ",") != want {
		t.Errorf("arches = %v, want them sorted as %s", r.Arches, want)
	}

	if err := Save(path, sample()); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(raw) {
		t.Error("saving the same facts twice produced different bytes")
	}
}

func TestLoadRejectsUnknownFieldsAndVersions(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "unknown.lock")
	if err := os.WriteFile(bad, []byte("version: 1\nreleases: []\ntoolchain: {}\nsurprise: yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Error("Load accepted an unknown field")
	}

	future := filepath.Join(dir, "future.lock")
	if err := os.WriteFile(future, []byte("version: 99\nreleases: []\ntoolchain: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(future); err == nil {
		t.Error("Load accepted a lockfile from a newer format version")
	}
}

func TestDiff(t *testing.T) {
	have := sample()

	if got := Diff(have, sample()); len(got) != 0 {
		t.Errorf("Diff of identical locks = %v, want nothing", got)
	}

	want := sample()
	want.Releases[0].Point = "25.12.6"
	want.Releases[0].Arches = []string{"x86_64", "aarch64_cortex-a53", "loongarch64_generic"}
	want.Toolchain.SDKRelease = "25.12.6"

	got := strings.Join(Diff(have, want), "\n")
	for _, sub := range []string{
		"25.12.5 -> 25.12.6",
		"+ 25.12 loongarch64_generic",
		"- 25.12 riscv64_generic",
		"no longer published upstream",
	} {
		if !strings.Contains(got, sub) {
			t.Errorf("diff does not mention %q:\n%s", sub, got)
		}
	}
}

// This is what --frozen-lock enforces: a new architecture upstream is welcome and
// still must not change what a release publishes without a human seeing it.
func TestCheckReportsWhatChanged(t *testing.T) {
	have := sample()
	want := sample()
	want.Releases[0].Arches = append(want.Releases[0].Arches, "loongarch64_generic")

	err := Check("owfeed.lock", have, want)
	if err == nil {
		t.Fatal("Check passed a lockfile that no longer matches upstream")
	}
	if !errors.Is(err, ErrStale) {
		t.Errorf("Check returned %T, which callers cannot recognise as staleness", err)
	}
	msg := err.Error()
	for _, sub := range []string{"loongarch64_generic", "owfeed lock --update"} {
		if !strings.Contains(msg, sub) {
			t.Errorf("message does not contain %q:\n%s", sub, msg)
		}
	}

	if err := Check("owfeed.lock", have, sample()); err != nil {
		t.Errorf("Check failed on an up-to-date lock: %v", err)
	}
}
