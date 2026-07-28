// Package release assembles what an author publishes so a feed can consume it.
//
// A release is a set of package files plus a signed inventory of them. The
// inventory is the point: without it a consumer has to trust whatever the hosting
// service says the release contains. It records which files belong to this release,
// how large each is and what it hashes to, and it is signed — so the right assets
// can be fetched without believing an API about them.
//
// The shape follows luci-theme-footstrap's manifest, which was built for exactly
// this and had already worked out the parts that are easy to miss.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/VizzleTF/owfeed/internal/usign"
)

// ManifestName is the inventory's filename.
const ManifestName = "manifest.txt"

// Format is the manifest's first line, so a reader can refuse a shape it does not
// know rather than misread one.
const Format = "owfeed-manifest 1"

// Options configure a release.
type Options struct {
	// Dir holds the packages, laid out as `owfeed build` leaves them.
	Dir string
	// Repo is the repository this release belongs to.
	//
	// It is in the manifest and readers are expected to check it. One key often
	// signs releases for several repositories, and without this line a manifest
	// lifted from one of them verifies perfectly against another: a signature proves
	// who wrote something, never what it is about.
	Repo string
	// Tag is the release tag.
	Tag string
	// Key signs the manifest and every package beside it.
	Key *usign.PrivateKey
	// Now is the release timestamp; zero means now.
	Now time.Time
}

// Result describes what was produced.
type Result struct {
	Manifest string
	Signed   []string
	KeyID    string
}

// Build writes the manifest and the detached signatures.
func Build(opts Options) (*Result, error) {
	pkgs, err := packages(opts.Dir)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("%s holds no packages to release", opts.Dir)
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", Format)
	fmt.Fprintf(&b, "repo %s\n", opts.Repo)
	fmt.Fprintf(&b, "tag %s\n", opts.Tag)
	fmt.Fprintf(&b, "version %s\n", strings.TrimPrefix(opts.Tag, "v"))
	fmt.Fprintf(&b, "date %s\n", now.UTC().Format(time.RFC3339))
	for _, p := range pkgs {
		fmt.Fprintf(&b, "pkg %s %s %s %s %d %s\n", p.name, p.format, p.arch, p.file, p.size, p.sum)
	}

	manifest := filepath.Join(opts.Dir, ManifestName)
	if err := os.WriteFile(manifest, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}

	// The manifest's signature is the one a manifest-aware reader needs. The
	// per-package ones stay anyway: a consumer already in the field may be fetching
	// a single asset and know nothing about manifests.
	signed := make([]string, 0, len(pkgs)+1)
	for _, p := range pkgs {
		signed = append(signed, p.path)
	}
	signed = append(signed, manifest)

	comment := fmt.Sprintf("%s %s", opts.Repo, opts.Tag)
	for _, f := range signed {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(f+".sig", opts.Key.Sign(data, comment), 0o644); err != nil {
			return nil, err
		}
	}

	return &Result{Manifest: manifest, Signed: signed, KeyID: opts.Key.ID.String()}, nil
}

type pkg struct {
	name, format, arch, file, path, sum string
	size                                int64
}

// packages walks the per-architecture directories `owfeed build` writes.
func packages(dir string) ([]pkg, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []pkg
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		arch := e.Name()
		files, err := os.ReadDir(filepath.Join(dir, arch))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			// Both lines in one release: 25.12 installs the .apk, 24.10 the .ipk, and
			// a maintainer who ships only one has abandoned half their users.
			if !strings.HasSuffix(f.Name(), ".apk") && !strings.HasSuffix(f.Name(), ".ipk") {
				continue
			}
			path := filepath.Join(dir, arch, f.Name())
			st, err := f.Info()
			if err != nil {
				return nil, err
			}
			sum, err := sha256File(path)
			if err != nil {
				return nil, err
			}
			out = append(out, pkg{
				name: packageName(f.Name()), format: strings.TrimPrefix(filepath.Ext(f.Name()), "."),
				arch: arch, file: f.Name(), path: path, sum: sum, size: st.Size(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].name != out[j].name {
			return out[i].name < out[j].name
		}
		return out[i].arch < out[j].arch
	})
	return out, nil
}

// packageName recovers a package's name from its filename. The two formats spell it
// differently — <name>-<version>.apk and <name>_<version>_<arch>.ipk — but a version
// always starts with a digit, so the seam is the last separator followed by one.
func packageName(file string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(file, ".apk"), ".ipk")
	for i := len(base) - 1; i > 0; i-- {
		if (base[i-1] == '-' || base[i-1] == '_') && base[i] >= '0' && base[i] <= '9' {
			return base[:i-1]
		}
	}
	return base
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
