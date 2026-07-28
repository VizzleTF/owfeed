package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/VizzleTF/owfeed/internal/apk"
	"github.com/VizzleTF/owfeed/internal/build"
	"github.com/VizzleTF/owfeed/internal/lock"
)

const defaultDist = "dist"

func (a *app) build(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed build", flag.ContinueOnError)
	fs.SetOutput(a.err)
	out := fs.String("o", defaultDist, "directory to write packages into")
	only := fs.String("package", "", "build only this package")
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
	tool, err := a.tool(ctx, l)
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

	built := 0
	for _, p := range c.Packages {
		if *only != "" && p.EffectiveName() != *only {
			continue
		}
		version, err := build.ResolveVersion(p, a.root())
		if err != nil {
			return wrap(exitBuild, err)
		}
		res, err := build.Build(ctx, tool, build.Request{
			Package:         p,
			Feed:            c.Feed,
			Root:            a.root(),
			Version:         version,
			OutDir:          *out,
			SourceDateEpoch: epoch,
		})
		if err != nil {
			return wrap(exitBuild, err)
		}
		a.logf("built %s", rel(res.File))
		for _, note := range res.Notes {
			a.logf("  note: %s", note)
		}
		built++
	}

	if built == 0 {
		return fail(exitConfig, "no package matched --package %q", *only)
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
