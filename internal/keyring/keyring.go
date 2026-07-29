// Package keyring builds the package that carries a feed's public key to routers.
//
// It exists for one reason: apk has no revocation, so the only thing owfeed can offer
// is making rotation cheap enough that a feed actually does it. A key installed by
// hand can only be replaced by hand, on every subscriber's router, which in practice
// means never — and a feed that cannot rotate is a feed whose key is permanent whether
// or not it has leaked.
//
// The mechanism is a property of APKv3: apk matches keys by the identity inside a
// signature and ignores the filename, so several keys coexist in /etc/apk/keys and
// installing a new one breaks nothing. So a package whose payload is the feed's public
// key, signed by the key subscribers ALREADY trust, delivers the next key through the
// ordinary `apk upgrade` path. The old key stays valid for the overlap window; the
// index is signed by both; afterwards the old one is dropped.
//
// What this package does NOT solve, and must not be read as solving: a router that is
// offline for the whole overlap window still ends up with a key it cannot replace, and
// a leaked key remains valid until every subscriber has upgraded. Rotation is cheaper
// here than elsewhere. It is not revocation.
package keyring

import (
	"crypto/ecdsa"
	"fmt"
	"os"
	"path/filepath"

	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/keys"
)

// NameFor is what the keyring package is called for a given feed.
func NameFor(feed string) string { return feed + "-keyring" }

// Stage writes the payload the keyring package is built from: the feed's public key,
// at the path apk reads keys from.
//
// The filename inside /etc/apk/keys is the key's own identity rather than the feed's
// name, and that is what makes rotation work rather than overwrite. A feed publishing
// its second key under the same filename would replace the first on upgrade, leaving a
// router that had not yet fetched the new index unable to verify anything; distinct
// names let both sit there until the old one is deliberately dropped.
func Stage(dir string, pub *ecdsa.PublicKey) (string, error) {
	id, err := keys.IdentityOf(pub)
	if err != nil {
		return "", err
	}
	pem, err := keys.MarshalPublic(pub)
	if err != nil {
		return "", err
	}
	keyDir := filepath.Join(dir, "etc", "apk", "keys")
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		return "", err
	}
	name := id.String() + ".pem"
	if err := os.WriteFile(filepath.Join(keyDir, name), pem, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// VersionFor builds the keyring package's version from a minor number.
//
// 1.<n>-r1: the major is fixed because nothing about this package's shape changes, the
// minor counts rotations, and the revision is there because apk wants one.
func VersionFor(minor int) string { return fmt.Sprintf("1.%d-r1", minor) }

// MinorOf reads the minor number back out of a version, returning 0 for anything it
// cannot parse — which makes a corrupted entry produce a version of 1, not a panic and
// not a version that silently repeats.
func MinorOf(version string) int {
	var major, minor, rev int
	if _, err := fmt.Sscanf(version, "%d.%d-r%d", &major, &minor, &rev); err != nil {
		return 0
	}
	return minor
}

// Package describes the keyring package for a feed.
//
// The version is derived from the key's identity, not from a date or a counter. A
// feed that rebuilds without rotating produces the same version, so subscribers see no
// upgrade for a package whose contents did not change; a feed that rotates produces a
// different one, and the upgrade appears. Nothing has to be remembered between runs.
func Package(feed config.Feed, version, filesDir string) (config.Package, error) {
	desc := feed.Title
	if desc == "" {
		desc = feed.Name
	}
	return config.Package{
		Name:        NameFor(feed.Name),
		Build:       config.BuildMkpkg,
		Arch:        config.PkgArch{List: []string{config.Noarch}},
		Version:     version,
		Files:       filesDir,
		Description: "Public signing key for the " + desc + " package feed",
		License:     feed.License,
		URL:         feed.Homepage,
		Maintainer:  feed.Maintainer,
	}, nil
}
