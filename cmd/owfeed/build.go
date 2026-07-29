package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"owfeed.org/owfeed/internal/apk"
	"owfeed.org/owfeed/internal/build"
	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/keyring"
	"owfeed.org/owfeed/internal/keys"
	"owfeed.org/owfeed/internal/lock"
)

const defaultDist = "dist"

func (a *app) build(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed build", flag.ContinueOnError)
	fs.SetOutput(a.err)
	out := fs.String("o", defaultDist, "directory to write packages into")
	onlyPkg := fs.String("package", "", "build only this package")
	onlyLine := fs.String("release", "", "build only this release line")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	c, err := a.loadConfig()
	if err != nil {
		return err
	}
	l, err := a.requireLock(ctx, c)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		return wrap(exitBuild, err)
	}

	epoch, err := sourceDateEpoch()
	if err != nil {
		return wrap(exitConfig, err)
	}

	// A feed that carries only other people's work builds nothing: every package is
	// fetched already built and signed by its author. Saying so and stopping is
	// right; fetching an apk toolchain to package zero things is not.
	if len(c.Packages) == 0 {
		a.logf("no packages to build; everything this feed carries is fetched already built")
		return nil
	}

	built := 0
	needAPK := false
	for _, r := range c.Releases {
		if *onlyLine != "" && r.Line != *onlyLine {
			continue
		}
		if r.Format == config.FormatAPK {
			needAPK = true
		}
	}

	// The apk toolchain is only fetched when an apk line is actually being built.
	// A feed publishing only for 24.10 has no use for it, and the 24.10 SDK does
	// not carry it anyway.
	var tool *apk.Tool
	if needAPK {
		if tool, err = a.tool(ctx, l); err != nil {
			return err
		}
	}

	// The keyring package is built like any other, and deliberately so: it is indexed,
	// signed and published through exactly the same path, so nothing about it is a
	// special case that could rot separately.
	//
	// It carries the feed's CURRENT public key. Its value is not this key — a
	// subscriber already has that, or they could not have installed anything — but the
	// next one: a rotation publishes a new keyring version signed by the key routers
	// still trust, and `apk upgrade` carries the replacement to them. Without it a
	// rotation reaches only the subscribers who read the announcement, which is a
	// rotation that does not happen.
	packages := c.Packages
	if *c.Signing.KeyringPackage {
		kp, cleanup, err := a.stageKeyring(c, l)
		if err != nil {
			return err
		}
		defer cleanup()
		packages = append(append([]config.Package(nil), packages...), kp)
	}

	for _, r := range c.Releases {
		if *onlyLine != "" && r.Line != *onlyLine {
			continue
		}
		for _, p := range packages {
			if *onlyPkg != "" && p.EffectiveName() != *onlyPkg {
				continue
			}
			// A package says which lines it belongs to; saying nothing means all of
			// them, which is right for anything that runs on both.
			if !p.PublishedOn(r.Line) {
				continue
			}

			version, err := build.ResolveVersion(p, a.root())
			if err != nil {
				return wrap(exitBuild, err)
			}
			for _, arch := range p.Arch.List {
				res, err := build.Build(ctx, tool, build.Request{
					Package:         p,
					Feed:            c.Feed,
					Root:            a.root(),
					Version:         version,
					Arch:            arch,
					OutDir:          *out,
					SourceDateEpoch: epoch,
					Format:          r.Format,
				})
				if err != nil {
					return wrap(exitBuild, err)
				}
				a.logf("built %s (%s)", rel(res.File), r.Line)
				for _, note := range res.Notes {
					a.logf("  note: %s", note)
				}
				built++
			}
		}
	}

	if built == 0 {
		return fail(exitConfig, "nothing to build: no package matched the given --package and --release")
	}
	// Packages leave here unsigned on purpose: the stage that runs build inputs is
	// not the stage that holds the signing key.
	a.logf("%d package(s) in %s, unsigned — run `owfeed sign %s` next", built, *out, *out)
	return nil
}

// tool resolves the host apk, acquiring the SDK toolchain pinned by the lockfile if
// it is not already cached.
func (a *app) tool(ctx context.Context, l *lock.Lock) (*apk.Tool, error) {
	if explicit := os.Getenv("OWFEED_APK"); explicit != "" {
		t, err := apk.Resolve(ctx, apk.Options{Explicit: explicit})
		return t, wrap(exitBuild, err)
	}

	release := l.Toolchain.SDKRelease
	if release == "" {
		return nil, fail(exitConflict, "owfeed.lock records no SDK release; run `owfeed lock --update`")
	}
	if a.noNetwork {
		a.debugf("using the cached toolchain for %s", release)
	}
	sdkDir, err := apk.Acquire(ctx, http.DefaultClient, a.cacheRoot, release)
	if err != nil {
		return nil, wrap(exitUpstream, err)
	}
	t, err := apk.Resolve(ctx, apk.Options{SDKDir: sdkDir, AllowContainer: true})
	if err != nil {
		return nil, wrap(exitBuild, err)
	}
	a.debugf("apk: %s (%s)", t.Version, t.Origin)
	return t, nil
}

// sourceDateEpoch reads the reproducible-builds convention. Without it a build
// carries the checkout's mtimes, so the same inputs produce different bytes on a
// laptop and in a fresh CI clone.
func sourceDateEpoch() (time.Time, error) {
	raw := os.Getenv("SOURCE_DATE_EPOCH")
	if raw == "" {
		return time.Time{}, nil
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(secs, 0), nil
}

// stageKeyring writes the keyring package's payload to a temporary directory and
// returns the package describing it.
//
// Staged rather than committed: the payload is one file derived from the signing key,
// so keeping a copy in the repository would be a second place for it to be wrong.
func (a *app) stageKeyring(c *config.Config, l *lock.Lock) (config.Package, func(), error) {
	key, err := a.signingKey(c)
	if err != nil {
		return config.Package{}, func() {}, err
	}
	// The version comes from the lockfile, which is where a rotation is recorded and
	// reviewed. Building without it would mean inventing a number, and a keyring
	// version invented per run is one that either never moves or moves for no reason.
	if l.Keyring == nil {
		return config.Package{}, func() {}, fail(exitConflict,
			"signing.keyring-package is on but %s records no keyring version; run `owfeed lock --update`",
			a.lockPath())
	}
	id, err := keys.IdentityOf(&key.PublicKey)
	if err != nil {
		return config.Package{}, func() {}, wrap(exitKey, err)
	}
	// A key that disagrees with the record is a rotation nobody wrote down. Building
	// anyway would publish the new key under the old version, which every router would
	// decline to install as an upgrade it already has.
	if l.Keyring.Identity != id.String() {
		return config.Package{}, func() {}, fail(exitConflict,
			"the signing key is %s but %s records %s for the keyring package\n"+
				"  run `owfeed lock --update` and commit the diff: a rotation is a fact worth seeing",
			id, a.lockPath(), l.Keyring.Identity)
	}
	version := l.Keyring.Version
	// Beside the config rather than in TMPDIR: `files:` is resolved against the
	// config's directory, and on macOS the system temp directory is on another volume,
	// where a relative path to it cannot be formed at all.
	const stageDir = ".owfeed-keyring"
	dir := filepath.Join(a.root(), stageDir)
	if err := os.RemoveAll(dir); err != nil {
		return config.Package{}, func() {}, wrap(exitBuild, err)
	}
	cleanup := func() { os.RemoveAll(dir) }

	if _, err := keyring.Stage(dir, &key.PublicKey); err != nil {
		cleanup()
		return config.Package{}, func() {}, wrap(exitBuild, err)
	}
	kp, err := keyring.Package(c.Feed, version, stageDir)
	if err != nil {
		cleanup()
		return config.Package{}, func() {}, wrap(exitBuild, err)
	}
	return kp, cleanup, nil
}
