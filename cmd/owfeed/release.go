package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/VizzleTF/owfeed/internal/release"
	"github.com/VizzleTF/owfeed/internal/usign"
)

// release is the author's side of a feed that carries other people's work: it turns
// a directory of built packages into something a feed can consume without trusting
// the hosting service — an inventory of what belongs to this release, signed.
func (a *app) release(args []string) error {
	fs := flag.NewFlagSet("owfeed release", flag.ContinueOnError)
	fs.SetOutput(a.err)
	keySpec := fs.String("key", "env:OWFEED_RELEASE_KEY", "usign signing key, as env:VAR or file:PATH")
	repo := fs.String("repo", envAny("GITHUB_REPOSITORY", "CI_PROJECT_PATH"), "the repository this release belongs to")
	tag := fs.String("tag", envAny("GITHUB_REF_NAME", "CI_COMMIT_TAG"), "the release tag")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	dir := defaultDist
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if *repo == "" {
		return fail(exitConfig, "--repo is required: the manifest records it and readers check it, "+
			"because one key often signs releases for several repositories and a signature says who wrote something, not what it is about")
	}
	if *tag == "" {
		return fail(exitConfig, "--tag is required")
	}

	key, err := loadReleaseKey(*keySpec)
	if err != nil {
		return wrap(exitKey, err)
	}

	res, err := release.Build(release.Options{
		Dir: dir, Repo: *repo, Tag: *tag, Key: key, Now: releaseDate(),
	})
	if err != nil {
		return wrap(exitBuild, err)
	}

	a.logf("wrote %s and signed %d file(s) with key %s", rel(res.Manifest), len(res.Signed), res.KeyID)
	a.logf("publish the whole directory: a manifest without the packages it names is not a release")
	return nil
}

// loadReleaseKey reads the usign key that signs a release. It is a different key
// from the one that signs packages: apk verifies EC signatures inside a package,
// while a release manifest is read by installers and by feeds, which speak usign.
func loadReleaseKey(spec string) (*usign.PrivateKey, error) {
	scheme, arg, _ := strings.Cut(spec, ":")
	switch scheme {
	case "env":
		pem := os.Getenv(arg)
		if pem == "" {
			return nil, fmt.Errorf("$%s is empty; it should hold the usign secret key", arg)
		}
		return usign.ParsePrivateKey([]byte(pem))
	case "file":
		b, err := os.ReadFile(arg)
		if err != nil {
			return nil, err
		}
		return usign.ParsePrivateKey(b)
	default:
		return nil, fmt.Errorf("--key %q has no source; use env:VAR or file:PATH", spec)
	}
}

// releaseDate is when the release was made, and deliberately not SOURCE_DATE_EPOCH.
//
// That variable exists so a package's identity depends on what it contains and
// nothing else -- apk hashes the payload and mkpkg records mtimes, so a fixed epoch
// keeps a rebuild of identical content from claiming to be new. A feed sets it to a
// constant for exactly that reason.
//
// A manifest is not a package. Its date says when a release was cut, and honouring
// SOURCE_DATE_EPOCH here put "2023-11-14" in a signed document describing a release
// made in 2026, because the feed's constant was in the environment. A signed
// statement asserting a date it knows to be false is worse than no date at all, and
// nothing depends on the manifest being byte-reproducible -- every hash it records
// already is.
func releaseDate() time.Time {
	if raw := os.Getenv("OWFEED_RELEASE_DATE"); raw != "" {
		if secs, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return time.Unix(secs, 0).UTC()
		}
	}
	return time.Now().UTC()
}

func envAny(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
