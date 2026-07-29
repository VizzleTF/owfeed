package build

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/meta"
)

// ResolveVersion produces the version for a package, from either the literal in the
// config or the source named by version-from.
//
// Reading the version from where it already lives is the point: a version repeated
// in owfeed.yml is a version that will disagree with the Makefile eventually, and
// the disagreement surfaces as a package that will not upgrade rather than as an
// error.
func ResolveVersion(p config.Package, root string) (string, error) {
	if p.Version != "" {
		return p.Version, validated(p.Version)
	}

	scheme, arg, _ := strings.Cut(p.VersionFrom, ":")
	switch scheme {
	case "makefile":
		v, err := versionFromMakefile(filepath.Join(root, arg))
		if err != nil {
			return "", err
		}
		return v, validated(v)
	case "file":
		b, err := os.ReadFile(filepath.Join(root, arg))
		if err != nil {
			return "", err
		}
		v := strings.TrimSpace(string(b))
		return v, validated(v)
	default:
		return "", fmt.Errorf("version-from: %q is not a version source; use makefile:PATH or file:PATH", p.VersionFrom)
	}
}

func validated(v string) error {
	if err := meta.ValidateVersion(v); err != nil {
		return err
	}
	return nil
}

// assignRE matches a make variable assignment of a plain value.
var assignRE = regexp.MustCompile(`^\s*(PKG_VERSION|PKG_RELEASE)\s*[:?]?=\s*(.*?)\s*$`)

// versionFromMakefile composes a version the way OpenWrt's package-defaults.mk does:
//
//	PKG_VERSION and PKG_RELEASE  -> PKG_VERSION-rPKG_RELEASE
//	PKG_VERSION only             -> PKG_VERSION
//	PKG_RELEASE only             -> PKG_RELEASE
func versionFromMakefile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var version, release string
	for _, line := range strings.Split(string(b), "\n") {
		m := assignRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// A value built from other make variables cannot be read without running
		// make, and guessing would produce a version that looks plausible and is
		// wrong. Say so instead.
		if strings.Contains(m[2], "$(") || strings.Contains(m[2], "${") {
			return "", fmt.Errorf("%s: %s is computed from other make variables (%s); "+
				"set `version:` in owfeed.yml, or point version-from at a file holding the literal", path, m[1], m[2])
		}
		switch m[1] {
		case "PKG_VERSION":
			version = m[2]
		case "PKG_RELEASE":
			release = m[2]
		}
	}

	switch {
	case version != "" && release != "":
		return version + "-r" + release, nil
	case version != "":
		return version, nil
	case release != "":
		return release, nil
	default:
		return "", fmt.Errorf("%s: neither PKG_VERSION nor PKG_RELEASE is set", path)
	}
}
