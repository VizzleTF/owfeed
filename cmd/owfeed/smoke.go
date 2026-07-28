package main

import (
	"context"
	"flag"

	"github.com/VizzleTF/owfeed/internal/smoke"
)

// smoke is the only check that asks apk rather than telling it. Everything else
// compares the feed against a description of how apk behaves; this installs it.
func (a *app) smoke(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed smoke", flag.ContinueOnError)
	fs.SetOutput(a.err)
	image := fs.String("image", "", "router image (default: derived from the release line)")
	arch := fs.String("arch", smoke.DefaultArch, "architecture to install")
	release := fs.String("release", "", "release line (default: the one owfeed.yml advertises)")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	out := defaultOut
	if fs.NArg() > 0 {
		out = fs.Arg(0)
	}

	c, err := a.loadConfig()
	if err != nil {
		return err
	}
	l, err := a.requireLock(ctx, c)
	if err != nil {
		return err
	}
	line := *release
	if line == "" {
		line = c.DefaultRelease().Line
	}

	res, err := smoke.Run(ctx, smoke.Options{
		Dir:          out,
		FeedName:     c.Feed.Name,
		Release:      line,
		PointRelease: l.Toolchain.SDKRelease,
		LayoutPath:   c.Layout.Path,
		Arch:         *arch,
		Image:        *image,
	})
	if err != nil {
		return wrap(exitCheck, err)
	}

	a.logf("installed %d package(s) from %s on %s", len(res.Installed), res.Arch, res.Image)
	for _, p := range res.Installed {
		a.logf("  %s", p)
	}
	if res.LocalInstall {
		a.logf("`apk add ./file.apk` works without --allow-untrusted, so LuCI's Upload Package flow can install these")
	} else {
		a.logf("note: `apk add ./file.apk` needed a flag, so LuCI's Upload Package flow cannot install these")
	}
	if res.WorldPin != "" {
		// Worth printing every time: it is the reason the install snippet must never
		// tell anyone to install the file directly.
		a.logf("a local install pinned it in /etc/apk/world as %q — such a package never upgrades from the feed again", res.WorldPin)
	}
	// No silent caps: one architecture was installed, and the report says so rather
	// than implying the whole matrix was exercised.
	a.logf("this covered %s only; the other architectures are covered by the index checks", res.Arch)
	return nil
}
