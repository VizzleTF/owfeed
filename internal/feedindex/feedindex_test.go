package feedindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VizzleTF/owfeed/internal/ipkindex"
)

// A Size field that will not parse used to become zero, because the error from
// Sscanf was dropped. Every check that compares a package against its recorded size
// then compared it against nothing — reporting a healthy feed as wrong, or a broken
// one as fine, depending on which way the comparison ran.
func TestUnparsableSizeIsAnError(t *testing.T) {
	dir := writeIPKIndex(t, "Size: not-a-number")
	_, err := ReadDir(dir)
	if err == nil {
		t.Fatal("ReadDir accepted a Size that is not a number")
	}
	if !strings.Contains(err.Error(), "not a number") {
		t.Errorf("error does not say what is wrong: %v", err)
	}
}

// The same index with a size that parses is read without complaint, so the test
// above is exercising the parse and not the fixture.
func TestParsableSizeIsRead(t *testing.T) {
	dir := writeIPKIndex(t, "Size: 4096")
	idx, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(idx.Entries))
	}
	if got := idx.Entries[0].Size; got != 4096 {
		t.Errorf("size = %d, want 4096", got)
	}
}

func writeIPKIndex(t *testing.T, sizeLine string) string {
	t.Helper()
	dir := t.TempDir()
	body := "Package: demo\nVersion: 1.0-r1\nArchitecture: x86_64\n" +
		"Filename: demo_1.0-r1_x86_64.ipk\nSHA256sum: " + strings.Repeat("0", 64) + "\n" +
		sizeLine + "\n\n"
	if err := os.WriteFile(filepath.Join(dir, ipkindex.IndexFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
