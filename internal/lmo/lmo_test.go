package lmo

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The golden files were produced by the real po2lmo, built from
// modules/luci-base/src/po2lmo.c, over luci-theme-footstrap's actual catalogues.
// A translation table that is merely close returns the wrong string, so the only
// useful assertion is byte equality with the tool LuCI's own build uses.
func TestCompileMatchesPo2lmo(t *testing.T) {
	for _, lang := range []string{"ru", "es"} {
		t.Run(lang, func(t *testing.T) {
			po, err := os.Open(filepath.Join("testdata", "footstrap-"+lang+".po"))
			if err != nil {
				t.Fatal(err)
			}
			defer po.Close()

			got, err := Compile(po)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", "footstrap-"+lang+".lmo"))
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(got, want) {
				t.Errorf("output differs from po2lmo: %d bytes vs %d", len(got), len(want))
				if i := firstDiff(got, want); i >= 0 {
					t.Errorf("first difference at byte %d: got %#x, want %#x", i, at(got, i), at(want, i))
				}
			}
		})
	}
}

// The trailer is the size of the data section, which is how a reader finds the
// index; getting it wrong makes every lookup read garbage.
func TestTrailerLocatesTheIndex(t *testing.T) {
	po, err := os.Open(filepath.Join("testdata", "footstrap-ru.po"))
	if err != nil {
		t.Fatal(err)
	}
	defer po.Close()

	out, err := Compile(po)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 4 {
		t.Fatal("output is too short to hold a trailer")
	}

	dataLen := binary.BigEndian.Uint32(out[len(out)-4:])
	indexLen := len(out) - 4 - int(dataLen)
	if indexLen <= 0 || indexLen%16 != 0 {
		t.Fatalf("index section is %d bytes, which is not a whole number of 16-byte entries", indexLen)
	}

	// Entries are sorted by key so a reader can binary-search them.
	var prev uint32
	for off := int(dataLen); off < len(out)-4; off += 16 {
		key := binary.BigEndian.Uint32(out[off:])
		if key < prev {
			t.Fatalf("index is not sorted: %#x follows %#x", key, prev)
		}
		prev = key
	}
}

// A catalogue with nothing translated produces no file at all, matching po2lmo,
// which unlinks its output rather than leaving an empty one for LuCI to load.
func TestEmptyCatalogueProducesNothing(t *testing.T) {
	got, err := Compile(strings.NewReader("# nothing here\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("Compile returned %d bytes for an empty catalogue", len(got))
	}
}

// Only \" and \\ are unescaped. \n in particular stays as the two characters it
// was written as, and the header parser depends on that.
func TestExtractString(t *testing.T) {
	tests := []struct{ line, want string }{
		{`msgid "plain"`, "plain"},
		{`msgstr "with \"quotes\""`, `with "quotes"`},
		{`msgstr "back\\slash"`, `back\slash`},
		{`msgstr "line\nbreak"`, `line\nbreak`},
		{`""`, ""},
	}
	for _, tc := range tests {
		got, ok := extractString(tc.line)
		if !ok {
			t.Errorf("extractString(%q) found no string", tc.line)
			continue
		}
		if got != tc.want {
			t.Errorf("extractString(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
	if _, ok := extractString(`# a comment "with quotes"`); ok {
		t.Error("extractString read a comment line")
	}
}

func TestPluralFormsFromHeader(t *testing.T) {
	header := `Project-Id-Version: \nContent-Type: text/plain; charset=UTF-8\n` +
		`Plural-Forms: nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : 2);\nLast-Translator: nobody\n`

	got, ok := pluralForms(header)
	if !ok {
		t.Fatal("pluralForms found nothing in a header that has one")
	}
	if got != "nplurals=3; plural=(n%10==1 && n%100!=11 ? 0 : 2);" {
		t.Errorf("pluralForms = %q", got)
	}
	if _, ok := pluralForms(`Project-Id-Version: 1\n`); ok {
		t.Error("pluralForms invented a formula")
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func at(b []byte, i int) any {
	if i >= len(b) {
		return "end of file"
	}
	return b[i]
}
