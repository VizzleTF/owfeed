package main

import (
	"context"
	"flag"
	"net/http"
	"path/filepath"

	"github.com/VizzleTF/owfeed/internal/arch"
	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/lock"
)

func (a *app) lock(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed lock", flag.ContinueOnError)
	fs.SetOutput(a.err)
	update := fs.Bool("update", false, "write the derived facts to owfeed.lock")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	c, err := a.loadConfig()
	if err != nil {
		return err
	}

	want, err := a.derive(ctx, c)
	if err != nil {
		return err
	}

	path := a.lockPath()
	have, loadErr := lock.Load(path)

	if !*update {
		if loadErr != nil {
			return fail(exitConflict, "%s: %v\n  run `owfeed lock --update` to create it", path, loadErr)
		}
		if err := lock.Check(path, have, want); err != nil {
			return err
		}
		a.logf("%s is up to date", filepath.Base(path))
		return nil
	}

	for _, line := range lock.Diff(have, want) {
		a.logf("%s", line)
	}
	if err := lock.Save(path, want); err != nil {
		return wrap(exitInternal, err)
	}
	a.logf("wrote %s", filepath.Base(path))
	return nil
}

// derive resolves everything the lockfile records: which point release each line
// pins to, and which architectures that release publishes.
func (a *app) derive(ctx context.Context, c *config.Config) (*lock.Lock, error) {
	l := &lock.Lock{Version: lock.Version}

	for _, r := range c.Releases {
		point, err := a.pointFor(ctx, c, r)
		if err != nil {
			return nil, err
		}

		var res *arch.Result
		if a.noNetwork {
			res, err = arch.Cached(a.cacheRoot, point)
		} else {
			res, err = arch.Derive(ctx, http.DefaultClient, a.cacheRoot, point)
		}
		if err != nil {
			return nil, wrap(exitUpstream, err)
		}

		l.Releases = append(l.Releases, lock.Release{
			Line: r.Line, Point: point, Source: res.Source, Arches: res.Arches,
		})
		if r.Default {
			l.Toolchain.SDKRelease = point
		}
	}
	if l.Toolchain.SDKRelease == "" && len(l.Releases) > 0 {
		l.Toolchain.SDKRelease = l.Releases[0].Point
	}
	return l, nil
}

// pointFor resolves a release line to the concrete point release to build against.
func (a *app) pointFor(ctx context.Context, c *config.Config, r config.Release) (string, error) {
	if c.Build.SDK.Release != config.LatestPoint {
		return c.Build.SDK.Release, nil
	}
	if a.noNetwork {
		return "", fail(exitUpstream, "build.sdk.release is %q and --no-network was given; "+
			"pin a concrete point release, or run without --no-network", config.LatestPoint)
	}
	point, err := arch.LatestPoint(ctx, http.DefaultClient, r.Line)
	if err != nil {
		return "", wrap(exitUpstream, err)
	}
	return point, nil
}

func (a *app) lockPath() string {
	return filepath.Join(a.root(), lock.Name)
}

// requireLock reads the lockfile, and under --frozen-lock also proves it still
// matches upstream. That check is the point of the flag: a new architecture
// upstream is welcome and must still not change what a release publishes without a
// human seeing it, because the set of architectures a feed covers is part of its
// contract with subscribers.
func (a *app) requireLock(ctx context.Context, c *config.Config) (*lock.Lock, error) {
	path := a.lockPath()
	l, err := lock.Load(path)
	if err != nil {
		return nil, fail(exitConflict, "%v\n  run `owfeed lock --update`", err)
	}
	if !a.frozenLock {
		return l, nil
	}
	want, err := a.derive(ctx, c)
	if err != nil {
		return nil, err
	}
	if err := lock.Check(path, l, want); err != nil {
		return nil, err
	}
	return l, nil
}
