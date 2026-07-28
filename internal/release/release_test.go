package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VizzleTF/owfeed/internal/usign"
)

// A router that installed an earlier release is running a reader that parses this
// file positionally, and it cannot be fixed remotely. luci-theme-footstrap's
// installer does:
//
//	$1=="pkg" && $2==name && $3==ext {print $4, $5, $6}
//
// so field 4 has to be the filename. It once was not: the architecture had been
// inserted at position 4, and every router already in the field would have read
// "noarch" as the asset to download and fetched a URL that 404s.
func TestManifestFieldOrderIsWhatDeployedReadersParse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "noarch/luci-theme-demo-1.0-r1.apk", "apk bytes")
	write(t, dir, "all/luci-theme-demo_1.0-r1_all.ipk", "ipk bytes")
	write(t, dir, NotesName, "# 1.0\n")

	res, err := Build(Options{
		Dir: dir, Repo: "VizzleTF/demo", Tag: "v1.0", Key: key(t),
		Now: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}

	var pkgs int
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "pkg" {
			continue
		}
		pkgs++
		if len(f) != 7 {
			t.Fatalf("pkg line has %d fields, want 7: %q", len(f), line)
		}
		// The five a deployed reader indexes into, in its order.
		if f[1] != "luci-theme-demo" {
			t.Errorf("field 2 = %q, want the package name", f[1])
		}
		if f[2] != "apk" && f[2] != "ipk" {
			t.Errorf("field 3 = %q, want the extension the reader matches on", f[2])
		}
		if !strings.HasSuffix(f[3], "."+f[2]) {
			t.Errorf("field 4 = %q, want the filename ending in .%s", f[3], f[2])
		}
		// A bare filename, never a path: it becomes both a URL and a name in the
		// router's working directory, and a reader that rejects anything with a
		// slash in it -- as it should -- would refuse the whole release.
		if strings.ContainsRune(f[3], '/') {
			t.Errorf("field 4 = %q, want a bare filename", f[3])
		}
		if _, err := os.Stat(filepath.Join(dir, f[6], f[3])); err != nil {
			t.Errorf("field 4 = %q, which is not a file in the release: %v", f[3], err)
		}
		if len(f[5]) != 64 {
			t.Errorf("field 6 = %q, want a sha256", f[5])
		}
		// The one they never had, which is why it goes last.
		if f[6] == "" {
			t.Errorf("field 7 is empty, want the architecture")
		}
	}
	if pkgs != 2 {
		t.Fatalf("manifest names %d packages, want 2", pkgs)
	}

	// The confirmation dialog shows the user these notes, so their digest has to be
	// under the same signature as everything else.
	if !strings.Contains(string(body), " "+NotesName+"\n") {
		t.Errorf("manifest has no notes line:\n%s", body)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == "notes" {
			if len(f) != 3 || len(f[1]) != 64 || f[2] != NotesName {
				t.Errorf("notes line is %q, want `notes <sha256> %s`", line, NotesName)
			}
		}
	}
}

// A release with no notes says nothing about them, rather than naming a file that
// is not there.
func TestManifestOmitsNotesWhenThereAreNone(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "noarch/demo-1.0-r1.apk", "apk bytes")

	res, err := Build(Options{Dir: dir, Repo: "VizzleTF/demo", Tag: "v1.0", Key: key(t)})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "notes ") {
		t.Errorf("manifest names notes that do not exist:\n%s", body)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func key(t *testing.T) *usign.PrivateKey {
	t.Helper()
	priv, err := usign.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

// Release assets are flat, and an apk's filename carries no architecture — in a feed
// the architecture is the directory. A package built for several architectures
// therefore produces several files with one name, and uploading them to one release
// means all but one silently do not exist.
func TestCollidingAssetNamesAreDisambiguated(t *testing.T) {
	dir := t.TempDir()
	arches := []string{"x86_64", "aarch64_generic", "mips_24kc"}
	for _, a := range arches {
		write(t, dir, a+"/demo-1.0-r1.apk", "apk for "+a)
		write(t, dir, a+"/demo_1.0-r1_"+a+".ipk", "ipk for "+a)
	}

	res, err := Build(Options{Dir: dir, Repo: "VizzleTF/demo", Tag: "v1.0", Key: key(t)})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}

	names := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) != 7 || f[0] != "pkg" {
			continue
		}
		file, arch := f[3], f[6]
		if other, dup := names[file]; dup {
			t.Fatalf("%s is claimed by both %s and %s; one would replace the other in the release", file, other, arch)
		}
		names[file] = arch
		// The architecture has to be recoverable, because the name a feed publishes
		// it under is the canonical one, not this.
		if !strings.Contains(file, arch) {
			t.Errorf("%s does not name its architecture %s", file, arch)
		}
		if _, err := os.Stat(filepath.Join(dir, arch, file)); err != nil {
			t.Errorf("manifest names %s but it is not on disk: %v", file, err)
		}
	}
	if len(names) != 6 {
		t.Fatalf("manifest names %d assets, want 6", len(names))
	}
}

// A package built for one architecture keeps the filename it has always had. An
// installer already on a router looks its asset up by name and cannot be fixed
// remotely, so renaming it would break every existing install.
func TestASingleArchitectureKeepsItsName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "noarch/luci-theme-demo-1.0-r1.apk", "apk")
	write(t, dir, "all/luci-theme-demo_1.0-r1_all.ipk", "ipk")

	res, err := Build(Options{Dir: dir, Repo: "VizzleTF/demo", Tag: "v1.0", Key: key(t)})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"luci-theme-demo-1.0-r1.apk", "luci-theme-demo_1.0-r1_all.ipk"} {
		if !strings.Contains(string(body), " "+want+" ") {
			t.Errorf("manifest no longer names %s:\n%s", want, body)
		}
	}
}

// An installer published beside the packages gets a signature but no manifest
// entry. It is not a package a feed ingests, and the signature is for a person
// checking what they are about to run as root -- not for the install chain, which
// cannot verify a script that arrives first.
func TestSignAlsoSignsWithoutEnteringTheManifest(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "noarch/luci-theme-demo-1.0-r1.apk", "apk")
	write(t, dir, "install.sh", "#!/bin/sh\n")

	res, err := Build(Options{
		Dir: dir, Repo: "VizzleTF/demo", Tag: "v1.0", Key: key(t),
		SignAlso: []string{"install.sh"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "install.sh.sig")); err != nil {
		t.Errorf("install.sh was not signed: %v", err)
	}
	body, err := os.ReadFile(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "install.sh") {
		t.Errorf("install.sh entered the manifest:\n%s", body)
	}
}

// A named file that is not there is a typo in a release workflow, and a release
// missing the installer it advertises is worse than a red build.
func TestSignAlsoRefusesAMissingFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "noarch/luci-theme-demo-1.0-r1.apk", "apk")

	_, err := Build(Options{
		Dir: dir, Repo: "VizzleTF/demo", Tag: "v1.0", Key: key(t),
		SignAlso: []string{"install.sh"},
	})
	if err == nil {
		t.Fatal("expected an error naming the missing file")
	}
	if !strings.Contains(err.Error(), "install.sh") {
		t.Errorf("error does not name the file: %v", err)
	}
}
