package badge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	root := t.TempDir()
	err := Write(root, []Package{
		{Name: "luci-theme-footstrap", Version: "0.11.6-r1", Releases: []string{"25.12", "24.10"}},
		{Name: "solo", Version: "1.0.0-r1", Releases: []string{"25.12"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := read(t, filepath.Join(root, Dir, "luci-theme-footstrap.json"))
	if got.SchemaVersion != 1 {
		t.Errorf("schemaVersion is %d; shields requires 1", got.SchemaVersion)
	}
	// The left-hand side is the same on every badge: a reader scanning a README
	// should see one name they can look up.
	if got.Label != Label {
		t.Errorf("label is %q, want %q", got.Label, Label)
	}
	if got.Message != "0.11.6-r1" {
		t.Errorf("version badge says %q, want the version", got.Message)
	}

	rel := read(t, filepath.Join(root, Dir, "luci-theme-footstrap-releases.json"))
	if rel.Label != Label {
		t.Errorf("releases badge label is %q, want %q", rel.Label, Label)
	}
	if rel.Message != "25.12 · 24.10" {
		t.Errorf("releases badge says %q, want both lines", rel.Message)
	}

	if _, err := os.Stat(filepath.Join(root, Dir, "solo-releases.json")); err != nil {
		t.Errorf("a package on one line still gets a releases badge: %v", err)
	}
}

// A package with no releases recorded gets a version badge and nothing else, rather
// than a badge whose message is empty.
func TestNoReleasesWritesNoReleaseBadge(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, []Package{{Name: "p", Version: "1.0-r1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, Dir, "p.json")); err != nil {
		t.Fatalf("version badge missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, Dir, "p-releases.json")); !os.IsNotExist(err) {
		t.Errorf("wrote a releases badge with nothing to say")
	}
}

// Nothing to publish means no directory, not an empty one that looks like a feed
// which lost its packages.
func TestEmptyWritesNothing(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, Dir)); !os.IsNotExist(err) {
		t.Errorf("created %s/ for a feed with no packages", Dir)
	}
}

func TestURL(t *testing.T) {
	got := URL("https://repo.owfeed.org/", "luci-theme-footstrap", "")
	want := "https://img.shields.io/endpoint?url=https://repo.owfeed.org/badge/luci-theme-footstrap.json"
	if got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
	if got := URL("https://repo.owfeed.org", "p", "-releases"); got !=
		"https://img.shields.io/endpoint?url=https://repo.owfeed.org/badge/p-releases.json" {
		t.Errorf("suffix not applied: %q", got)
	}
}

func read(t *testing.T, path string) Endpoint {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var e Endpoint
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return e
}
