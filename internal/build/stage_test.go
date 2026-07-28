package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile does not apply the mode to an existing file, and umask can clear
	// bits on a new one.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestCopyTree(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "payload")

	write(t, filepath.Join(src, "etc/config/demo"), "config demo\n", 0o644)
	write(t, filepath.Join(src, "usr/bin/tool"), "#!/bin/sh\n", 0o755)
	if err := os.Symlink("../../usr/bin/tool", filepath.Join(src, "etc/config/link")); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst, time.Time{}); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	st, err := os.Stat(filepath.Join(dst, "usr/bin/tool"))
	if err != nil {
		t.Fatal(err)
	}
	// An executable that arrives unexecutable is a package that installs and then
	// does nothing.
	if st.Mode().Perm() != 0o755 {
		t.Errorf("tool mode = %04o, want 0755", st.Mode().Perm())
	}

	target, err := os.Readlink(filepath.Join(dst, "etc/config/link"))
	if err != nil {
		t.Fatalf("symlink was not copied as a symlink: %v", err)
	}
	if target != "../../usr/bin/tool" {
		t.Errorf("symlink target = %q, want it preserved verbatim", target)
	}
}

func TestCopyTreeRejects(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, src string)
		wantSub string
	}{
		{
			name: "CONTROL directory",
			setup: func(t *testing.T, src string) {
				write(t, filepath.Join(src, "CONTROL/control"), "Package: x\n", 0o644)
			},
			wantSub: "ipk metadata",
		},
		{
			name: "hand-staged sidecars",
			setup: func(t *testing.T, src string) {
				write(t, filepath.Join(src, metaDir, "x.list"), "/etc\n", 0o644)
			},
			wantSub: "owfeed generates those files",
		},
		{
			name: "world-writable file",
			setup: func(t *testing.T, src string) {
				write(t, filepath.Join(src, "etc/config/demo"), "x\n", 0o666)
			},
			wantSub: "world-writable",
		},
		{
			// The most damaging shape of mistake this path allows: the package
			// builds, installs and looks right, and has no translations.
			name: "gettext sources instead of compiled catalogues",
			setup: func(t *testing.T, src string) {
				write(t, filepath.Join(src, "usr/lib/lua/luci/i18n/theme.ru.po"), "msgid \"\"\n", 0o644)
			},
			wantSub: "po2lmo",
		},
		{
			name: "macOS metadata",
			setup: func(t *testing.T, src string) {
				write(t, filepath.Join(src, "www/luci-static/.DS_Store"), "\x00", 0o644)
			},
			wantSub: "macOS",
		},
		{
			name: "stylesheet source",
			setup: func(t *testing.T, src string) {
				write(t, filepath.Join(src, "www/luci-static/demo/style.scss"), "body{}\n", 0o644)
			},
			wantSub: "compiled CSS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			tc.setup(t, src)
			err := copyTree(src, filepath.Join(t.TempDir(), "payload"), time.Time{})
			if err == nil {
				t.Fatalf("copyTree accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestWriteSidecars(t *testing.T) {
	payload := t.TempDir()
	write(t, filepath.Join(payload, "etc/config/demo"), "config demo\n", 0o644)
	write(t, filepath.Join(payload, "usr/lib/lua/z.lua"), "return 1\n", 0o644)
	write(t, filepath.Join(payload, "etc/init.d/demo"), "#!/bin/sh\n", 0o755)

	if err := writeSidecars(payload, "demo", []string{"/etc/config/demo"}, time.Time{}); err != nil {
		t.Fatalf("writeSidecars: %v", err)
	}

	// The list is generated before .conffiles and .conffiles_static exist and is
	// moved into place afterwards, so it names neither them nor itself. This
	// reproduces package-pack.mk; a feed whose packages disagree with the official
	// ones about their own file lists would be the worse outcome.
	got := read(t, filepath.Join(payload, metaDir, "demo.list"))
	want := "/etc/config/demo\n/etc/init.d/demo\n/usr/lib/lua/z.lua\n"
	if got != want {
		t.Errorf("demo.list =\n%q\nwant\n%q", got, want)
	}

	line := read(t, filepath.Join(payload, metaDir, "demo.conffiles_static"))
	if !strings.HasPrefix(line, "/etc/config/demo ") || !strings.HasSuffix(line, "\n") {
		t.Errorf("conffiles_static = %q, want `<path> <sha256>`", line)
	}
	if fields := strings.Fields(line); len(fields) != 2 || len(fields[1]) != 64 {
		t.Errorf("conffiles_static = %q, want exactly a path and a 64-hex digest", line)
	}
	if sum, err := sha256File(filepath.Join(payload, "etc/config/demo")); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(line, sum) {
		t.Errorf("conffiles_static digest does not match the shipped file: %q", line)
	}
}

// A conffile that is declared and not shipped is the failure this catches: sysupgrade
// reads .conffiles_static to decide what to carry across an upgrade, so the mistake
// costs the user their configuration and reports nothing at build time.
func TestWriteSidecarsRejectsMissingConffile(t *testing.T) {
	payload := t.TempDir()
	write(t, filepath.Join(payload, "usr/lib/x"), "x\n", 0o644)

	err := writeSidecars(payload, "demo", []string{"/etc/config/absent"}, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("writeSidecars = %v, want a complaint about the missing conffile", err)
	}
}

func TestVersionFromMakefile(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name, content, want, wantErr string
	}{
		{
			name:    "version and release",
			content: "PKG_NAME:=demo\nPKG_VERSION:=1.2.3\nPKG_RELEASE:=4\n",
			want:    "1.2.3-r4",
		},
		{
			name:    "release only",
			content: "PKG_NAME:=luci-theme-x\nPKG_RELEASE:=7\n",
			want:    "7",
		},
		{
			name:    "version only",
			content: "PKG_VERSION:=2026.07.28\n",
			want:    "2026.07.28",
		},
		{
			name:    "computed value",
			content: "PKG_VERSION:=$(shell git describe)\nPKG_RELEASE:=1\n",
			wantErr: "computed from other make variables",
		},
		{
			name:    "neither",
			content: "PKG_NAME:=demo\n",
			wantErr: "neither PKG_VERSION nor PKG_RELEASE",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			write(t, path, tc.content, 0o644)

			got, err := versionFromMakefile(path)
			switch {
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("= %q, %v; want an error mentioning %q", got, err, tc.wantErr)
				}
			case err != nil:
				t.Fatalf("versionFromMakefile: %v", err)
			case got != tc.want:
				t.Errorf("= %q, want %q", got, tc.want)
			}
		})
	}
}

// The wrappers are the reason a mkpkg-built package behaves like an SDK-built one:
// without default_postinst the package's uci-defaults never run and its init scripts
// are never enabled, and nothing reports a failure.
func TestScriptWrappers(t *testing.T) {
	stage := t.TempDir()
	root := t.TempDir()
	write(t, filepath.Join(root, "post.sh"), "#!/bin/sh\necho mine\n", 0o755)

	scripts, err := writeScripts(stage, "demo", map[string]string{"post-install": "post.sh"}, root)
	if err != nil {
		t.Fatalf("writeScripts: %v", err)
	}

	byType := map[string]string{}
	for _, s := range scripts {
		byType[s.typ] = read(t, filepath.Join(stage, filepath.FromSlash(s.path)))
	}

	post, ok := byType["post-install"]
	if !ok {
		t.Fatal("no post-install script was generated")
	}
	for _, want := range []string{"default_postinst", `export pkgname="demo"`, "echo mine"} {
		if !strings.Contains(post, want) {
			t.Errorf("post-install does not contain %q:\n%s", want, post)
		}
	}
	if strings.Count(post, "#!") != 1 {
		t.Errorf("post-install has a second shebang from the user's script:\n%s", post)
	}
	if up := byType["post-upgrade"]; !strings.Contains(up, "PKG_UPGRADE=1") || !strings.Contains(up, "echo mine") {
		t.Errorf("post-upgrade should be the post-install steps with PKG_UPGRADE set:\n%s", up)
	}
	if pre := byType["pre-deinstall"]; !strings.Contains(pre, "default_prerm") {
		t.Errorf("pre-deinstall does not call default_prerm:\n%s", pre)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
