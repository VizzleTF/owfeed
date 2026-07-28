package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// metaDir is where OpenWrt keeps a package's sidecar metadata inside the payload.
const metaDir = "lib/apk/packages"

// copyTree copies a staged rootfs into the payload directory.
//
// The user's directory is never written to. Sidecar metadata has to live inside the
// payload, and adding it in place would both dirty a source tree and, on the next
// run, feed the previous run's generated files back in as package content.
func copyTree(src, dst string, epoch time.Time) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		out := filepath.Join(dst, rel)
		slash := filepath.ToSlash(rel)

		switch {
		case d.IsDir():
			// A CONTROL directory is ipk-era metadata. apk carries none of it, and
			// OpenWrt's own packaging fails the build rather than ship it.
			if slash == "CONTROL" {
				return fmt.Errorf("payload contains CONTROL/, which is ipk metadata; " +
					"apk keeps package metadata in the index and in " + metaDir + "/, not in the payload")
			}
			if slash == metaDir {
				return fmt.Errorf("payload already contains %s/; owfeed generates those files, "+
					"so a copy staged by hand would be overwritten or duplicated", metaDir)
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(out, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, out)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			mode := info.Mode().Perm()
			// A world-writable file in a package is writable by every process on the
			// router once installed. There is no packaging reason to ship one.
			if mode&0o002 != 0 {
				return fmt.Errorf("/%s is world-writable (mode %04o); chmod it before packaging", slash, mode)
			}
			if err := copyFile(path, out, mode); err != nil {
				return err
			}
			return stamp(out, epoch)
		default:
			// Sockets, fifos and device nodes. mkpkg can record them, but nothing
			// gets here by accident, and copying one silently is worse than saying so.
			return fmt.Errorf("/%s is a %s, which owfeed does not package", slash, d.Type())
		}
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// stamp normalises a file's mtime so the same inputs produce the same package.
// mkpkg records mtimes, so without this a fresh CI checkout and a laptop working
// copy build byte-different packages from identical content.
func stamp(path string, epoch time.Time) error {
	if epoch.IsZero() {
		return nil
	}
	return os.Chtimes(path, epoch, epoch)
}

// writeSidecars produces the metadata OpenWrt keeps inside the payload.
//
// The generation order matches package-pack.mk exactly, and the order is visible in
// the output: .list is built before .conffiles and .conffiles_static exist, so it
// does not mention them or itself. That is upstream's behaviour, and a feed whose
// packages disagree with the official ones about their own file lists is a worse
// outcome than a tidier list.
func writeSidecars(payload, name string, conffiles []string, epoch time.Time) error {
	dir := filepath.Join(payload, filepath.FromSlash(metaDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	list, err := payloadFiles(payload)
	if err != nil {
		return err
	}
	if err := writeMeta(filepath.Join(dir, name+".list"), strings.Join(list, "\n")+"\n", epoch); err != nil {
		return err
	}

	if len(conffiles) == 0 {
		return nil
	}
	sorted := append([]string(nil), conffiles...)
	sort.Strings(sorted)
	if err := writeMeta(filepath.Join(dir, name+".conffiles"), strings.Join(sorted, "\n")+"\n", epoch); err != nil {
		return err
	}

	// .conffiles_static is what sysupgrade reads to decide which configuration files
	// survive an upgrade. A package that ships /etc/config/foo without listing it
	// here loses the user's settings on every firmware update, and nothing warns.
	var static strings.Builder
	for _, cf := range sorted {
		p := filepath.Join(payload, filepath.FromSlash(strings.TrimPrefix(cf, "/")))
		sum, err := sha256File(p)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("conffiles lists %s, which the payload does not contain", cf)
			}
			return err
		}
		fmt.Fprintf(&static, "%s %s\n", cf, sum)
	}
	return writeMeta(filepath.Join(dir, name+".conffiles_static"), static.String(), epoch)
}

func writeMeta(path, content string, epoch time.Time) error {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	return stamp(path, epoch)
}

// payloadFiles reproduces `find . -type f,l -printf "/%P\n" | sort` under LC_ALL=C.
func payloadFiles(payload string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(payload, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(payload, path)
		if err != nil {
			return err
		}
		out = append(out, "/"+filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	// LC_ALL=C sort is a byte sort, which is what sort.Strings does.
	sort.Strings(out)
	return out, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
