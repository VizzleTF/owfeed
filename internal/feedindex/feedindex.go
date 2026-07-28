// Package feedindex reads a published index, whichever package manager wrote it.
//
// The checks that matter — does every entry resolve to a file, is that file the
// size the index promised, is the index signed by the key subscribers install —
// are the same questions for apk and for opkg. Only the answers are stored
// differently. Reading both into one shape means the checks are one piece of code
// rather than two that drift, and a feed serving both lines is held to the same
// standard on both.
//
// What the two do not share is worth stating, because it is where a naive
// abstraction would lie:
//
//   - apk records a payload identity (`hashes`) and the package's file size. The
//     filename is not stored at all; apk derives it from the name and version.
//   - opkg records the filename, the file's size and the file's SHA256. There is
//     no payload identity, and no per-package signature: opkg's trust rests on the
//     index alone.
package feedindex

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/VizzleTF/owfeed/internal/ipkindex"
	"github.com/VizzleTF/owfeed/internal/usign"
)

// Format names a package manager.
const (
	APK = "apk"
	IPK = "ipk"
)

// Entry is one package as its index describes it.
type Entry struct {
	Name    string
	Version string
	Arch    string
	// File is the name the package must be published under, beside the index.
	File string
	// Size is what the index says the file weighs.
	Size int64
	// SHA256 is the file's hash, when the format records one. apk does not: it
	// records a payload identity instead, which is a different claim.
	SHA256 string
	// Identity is apk's payload hash, when the format records one.
	Identity string
}

// Index is a published index, read back.
type Index struct {
	Format  string
	Entries []Entry
	// Size is the index's own size, which every subscriber downloads on every
	// update.
	Size int64
	// Raw is the index's bytes, for a signature check.
	Raw []byte
}

// ReadDir reads whichever index a published directory holds.
func ReadDir(dir string) (*Index, error) {
	switch {
	case exists(filepath.Join(dir, ipkindex.IndexFile)):
		return readIPK(dir)
	case exists(filepath.Join(dir, "index.json")):
		return readAPK(dir)
	}
	return nil, fmt.Errorf("%s holds neither an apk nor an opkg index", dir)
}

// readAPK reads the JSON rendering owfeed writes beside packages.adb. It is the
// same content as the binary index, and reading it needs no apk toolchain, which
// matters for a check that has to run wherever the tree is.
func readAPK(dir string) (*Index, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Packages []struct {
			Name     string `json:"name"`
			Version  string `json:"version"`
			Arch     string `json:"arch"`
			Hashes   string `json:"hashes"`
			FileSize int64  `json:"file-size"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s/index.json: %w", dir, err)
	}

	adb, err := os.ReadFile(filepath.Join(dir, "packages.adb"))
	if err != nil {
		return nil, err
	}

	idx := &Index{Format: APK, Size: int64(len(adb)), Raw: adb}
	for _, p := range doc.Packages {
		idx.Entries = append(idx.Entries, Entry{
			Name: p.Name, Version: p.Version, Arch: p.Arch,
			// apk stores no filename: it builds one from the name and version, so
			// that is the name the file has to be published under.
			File:     p.Name + "-" + p.Version + ".apk",
			Size:     p.FileSize,
			Identity: p.Hashes,
		})
	}
	return idx, nil
}

// readIPK parses the text index opkg reads.
func readIPK(dir string) (*Index, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ipkindex.IndexFile))
	if err != nil {
		return nil, err
	}
	idx := &Index{Format: IPK, Size: int64(len(raw)), Raw: raw}
	for _, stanza := range strings.Split(string(raw), "\n\n") {
		if strings.TrimSpace(stanza) == "" {
			continue
		}
		e := Entry{}
		for _, line := range strings.Split(stanza, "\n") {
			key, value, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			switch key {
			case "Package":
				e.Name = value
			case "Version":
				e.Version = value
			case "Architecture":
				e.Arch = value
			case "Filename":
				e.File = value
			case "SHA256sum":
				e.SHA256 = value
			case "Size":
				fmt.Sscanf(value, "%d", &e.Size)
			}
		}
		if e.Name != "" {
			idx.Entries = append(idx.Entries, e)
		}
	}
	return idx, nil
}

// CheckCompressed reports whether the gzipped copy opkg actually downloads matches
// the index the signature covers.
//
// They are separate files, and a publisher that regenerates one without the other
// serves a feed whose signature is valid and whose contents are stale.
func CheckCompressed(dir string, idx *Index) error {
	if idx.Format != IPK {
		return nil
	}
	blob, err := os.ReadFile(filepath.Join(dir, ipkindex.IndexGzFile))
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return err
	}
	defer gz.Close()

	plain, err := io.ReadAll(gz)
	if err != nil {
		return err
	}
	if !bytes.Equal(plain, idx.Raw) {
		return fmt.Errorf("%s does not decompress to %s", ipkindex.IndexGzFile, ipkindex.IndexFile)
	}
	return nil
}

// VerifyUsign checks an opkg index against a public key.
func VerifyUsign(dir string, idx *Index, pub *usign.PublicKey) error {
	blob, err := os.ReadFile(filepath.Join(dir, ipkindex.IndexSigFile))
	if err != nil {
		return err
	}
	sig, err := usign.ParseSignature(blob)
	if err != nil {
		return err
	}
	// Over the uncompressed index: opkg downloads Packages.gz and verifies against
	// Packages, and signing the compressed copy produces a feed every router rejects.
	return usign.Verify(pub, sig, idx.Raw)
}

// SHA256 hashes a file, for the formats that record one.
func SHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
