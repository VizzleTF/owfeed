// Package ipkindex builds the repository index opkg reads on 24.10 and earlier.
//
// It is a different shape from apk's in every respect that matters, and the
// differences are the sort that produce a feed which looks fine and does not work:
//
//   - The index is text, not a binary container: stanzas of control fields with
//     Filename, Size and SHA256sum inserted before the description.
//   - opkg fetches Packages.gz, but the signature covers the *uncompressed*
//     Packages. Signing the compressed file produces a feed every router rejects.
//   - The signature is usign, not the EC scheme apk uses, so a feed serving both
//     lines signs with two different keys.
//   - The repository line names a *directory*; opkg appends the filename itself.
//     apk's ndx line names the index file. Each is wrong for the other.
//   - The trusted key is installed as /etc/opkg/keys/<key id>, where the filename
//     is the id. apk matches on the identity inside the signature and ignores the
//     name.
package ipkindex

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/VizzleTF/owfeed/internal/usign"
)

// File names in a published directory.
const (
	IndexFile      = "Packages"
	IndexGzFile    = "Packages.gz"
	IndexSigFile   = "Packages.sig"
	packagesPerDir = "*.ipk"
)

// Options configure an index build.
type Options struct {
	// Dir holds the .ipk files and receives the index.
	Dir string
	// Key signs the index. opkg refuses an unsigned feed by default:
	// `option check_signature` is on in the stock /etc/opkg.conf.
	Key *usign.PrivateKey
	// Comment goes in the signature's header line.
	Comment string
}

// Result describes what was written.
type Result struct {
	Packages []string
	Size     int64
	KeyID    string
}

// Build writes Packages, Packages.gz and Packages.sig.
func Build(opts Options) (*Result, error) {
	files, err := filepath.Glob(filepath.Join(opts.Dir, packagesPerDir))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s contains no .ipk files to index", opts.Dir)
	}
	sort.Strings(files)

	var index bytes.Buffer
	var names []string
	for _, f := range files {
		stanza, name, err := entry(f)
		if err != nil {
			return nil, err
		}
		index.WriteString(stanza)
		index.WriteString("\n")
		names = append(names, name)
	}

	plain := index.Bytes()
	if err := os.WriteFile(filepath.Join(opts.Dir, IndexFile), plain, 0o644); err != nil {
		return nil, err
	}

	var gzBuf bytes.Buffer
	// No name or timestamp in the gzip header: they would make two indexes over
	// identical content differ, and the compressed copy is what every subscriber
	// downloads on every update.
	gz, err := gzip.NewWriterLevel(&gzBuf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(plain); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opts.Dir, IndexGzFile), gzBuf.Bytes(), 0o644); err != nil {
		return nil, err
	}

	// Over the uncompressed index. Verified against OpenWrt's own 24.10 feed: its
	// Packages.sig checks out against Packages and not against Packages.gz.
	if opts.Key == nil {
		return nil, fmt.Errorf("no signing key: opkg has check_signature on by default, so an unsigned index is a feed nobody can use")
	}
	sig := opts.Key.Sign(plain, opts.Comment)
	if err := os.WriteFile(filepath.Join(opts.Dir, IndexSigFile), sig, 0o644); err != nil {
		return nil, err
	}

	return &Result{Packages: names, Size: int64(len(plain)), KeyID: opts.Key.ID.String()}, nil
}

// entry renders one package's stanza, following scripts/ipkg-make-index.sh: the
// package's own control file with Filename, Size and SHA256sum inserted directly
// before the description.
//
// Before the description and not after, because a description continues onto
// indented following lines: a field written after it would be read as more prose.
func entry(path string) (string, string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	control, err := controlOf(body)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", filepath.Base(path), err)
	}

	sum := sha256.Sum256(body)
	inserted := fmt.Sprintf("Filename: %s\nSize: %d\nSHA256sum: %s\n",
		filepath.Base(path), len(body), hex.EncodeToString(sum[:]))

	if i := strings.Index(control, "\nDescription:"); i >= 0 {
		return control[:i+1] + inserted + control[i+1:], filepath.Base(path), nil
	}
	// A package with no description still belongs in the index.
	return strings.TrimRight(control, "\n") + "\n" + inserted, filepath.Base(path), nil
}

// controlOf pulls ./control out of an ipk's control.tar.gz.
func controlOf(ipk []byte) (string, error) {
	inner, err := member(ipk, "./control.tar.gz")
	if err != nil {
		return "", err
	}
	control, err := member(inner, "./control")
	if err != nil {
		return "", err
	}
	return string(control), nil
}

// member reads one file out of a gzipped tar.
func member(blob []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no %s inside", want)
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimPrefix(hdr.Name, "./") == strings.TrimPrefix(want, "./") {
			return io.ReadAll(tr)
		}
	}
}
