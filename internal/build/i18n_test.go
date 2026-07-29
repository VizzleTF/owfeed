package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"owfeed.org/owfeed/internal/config"
)

// stagePo copies the real footstrap catalogues into a source tree shaped the way
// both LuCI's po/ convention and the i18n/ variant lay them out.
func stagePo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, lang := range []string{"ru", "es"} {
		src := filepath.Join("..", "lmo", "testdata", "footstrap-"+lang+".po")
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "i18n", lang)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "footstrap.po"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A templates directory of .pot files sits beside the languages in every real
	// layout, and must not be mistaken for one.
	if err := os.MkdirAll(filepath.Join(root, "i18n", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "i18n", "templates", "footstrap.pot"), []byte("msgid \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCompileCatalogues(t *testing.T) {
	root := stagePo(t)
	payload := t.TempDir()

	got, err := compileCatalogues(payload, root, &config.I18n{From: "i18n"}, time.Time{})
	if err != nil {
		t.Fatalf("compileCatalogues: %v", err)
	}
	// The basename defaults to the .po file's own name, which is what luci.mk does.
	want := []string{
		"/usr/lib/lua/luci/i18n/footstrap.es.lmo",
		"/usr/lib/lua/luci/i18n/footstrap.ru.lmo",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("compiled %v, want %v", got, want)
	}
	// templates/ holds .pot files and is not a language.
	for _, p := range got {
		if strings.Contains(p, "templates") {
			t.Errorf("treated the templates directory as a language: %s", p)
		}
	}
	for _, p := range want {
		st, err := os.Stat(filepath.Join(payload, strings.TrimPrefix(p, "/")))
		if err != nil {
			t.Fatalf("%s was not written: %v", p, err)
		}
		if st.Size() < 1000 {
			t.Errorf("%s is %d bytes, which is too small to be a real catalogue", p, st.Size())
		}
	}
}

// The basename is the one part that must be settable. LuCI's loader globs
// *.<lang>.lmo so any name is found, but a package that previously shipped
// translations through a separate luci-i18n-<name>-<lang> package must not reuse
// that path: a router upgrading from it still owns the file, and a conflict makes
// apk refuse the upgrade.
func TestCataloguesBasenameOverride(t *testing.T) {
	root := stagePo(t)
	payload := t.TempDir()

	got, err := compileCatalogues(payload, root, &config.I18n{From: "i18n", Basename: "footstrap-theme"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if !strings.Contains(p, "footstrap-theme.") {
			t.Errorf("basename override was ignored: %s", p)
		}
	}
}

func TestCompileCataloguesRejectsEmptyDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "i18n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := compileCatalogues(t.TempDir(), root, &config.I18n{From: "i18n"}, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "no <lang>/*.po") {
		t.Fatalf("compileCatalogues = %v, want a complaint about the empty directory", err)
	}
}
