package main

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"

	"owfeed.org/owfeed/internal/badge"
	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/feedindex"
	"owfeed.org/owfeed/internal/index"
	"owfeed.org/owfeed/internal/keys"
)

const defaultOut = "out"

func (a *app) index(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed index", flag.ContinueOnError)
	fs.SetOutput(a.err)
	out := fs.String("o", defaultOut, "directory to write the publishable tree into")
	onlyLine := fs.String("release", "", "index only this release line")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	dist := defaultDist
	if fs.NArg() > 0 {
		dist = fs.Arg(0)
	}

	c, err := a.loadConfig()
	if err != nil {
		return err
	}
	l, err := a.requireLock(ctx, c)
	if err != nil {
		return err
	}
	// Badge data is collected across both formats: a package on 24.10 only would
	// otherwise get no badge, and one on both lines would claim only 25.12.
	badges := map[string]badge.Package{}

	// One command, every line the config declares. A feed serving both is two feeds
	// under one URL, and building them separately is how one of them goes stale.
	lines := 0
	for _, r := range c.Releases {
		if *onlyLine != "" && r.Line != *onlyLine {
			continue
		}
		if r.Format == config.FormatIPK {
			if err := a.indexIPK(c, l, r, dist, *out, badges); err != nil {
				return err
			}
			lines++
		}
	}

	var apkLines []config.Release
	for _, r := range c.Releases {
		if (*onlyLine == "" || r.Line == *onlyLine) && r.Format == config.FormatAPK {
			apkLines = append(apkLines, r)
		}
	}
	if len(apkLines) == 0 {
		if lines == 0 {
			return fail(exitConfig, "no release line matched --release %q", *onlyLine)
		}
		// The ipk side already wrote its key and its trees; its badges are still ours
		// to write, because this is the only place both formats have been seen.
		return a.writeBadges(*out, badges)
	}

	tool, err := a.tool(ctx, l)
	if err != nil {
		return err
	}

	tree, err := index.Tree(dist)
	if err != nil {
		return wrap(exitIndex, err)
	}
	if len(tree) == 0 {
		return fail(exitIndex, "%s holds no packages; run `owfeed build` first", dist)
	}

	key, err := a.signingKey(c)
	if err != nil {
		return err
	}
	keyDir, cleanupKey, err := a.stageKey(key)
	if err != nil {
		return err
	}
	defer cleanupKey()

	// apk verifies the packages it indexes against this directory, so the index step
	// checks the signing step's work instead of waving it through. adbsign reports
	// its failures and exits 0 regardless, so something has to.
	trustDir, err := keys.StagePublic(os.TempDir(), c.Feed.Name+".pem", &key.PublicKey)
	if err != nil {
		return wrap(exitKey, err)
	}
	defer os.RemoveAll(trustDir)

	signer, err := index.NewSigner(keyDir, keyFileName, key)
	if err != nil {
		return wrap(exitKey, err)
	}

	seenLine := map[string]bool{}

	for _, r := range apkLines {
		lr, ok := l.Release(r.Line)
		if !ok {
			return fail(exitConflict, "owfeed.lock has no entry for release line %s; run `owfeed lock --update`", r.Line)
		}
		total := 0
		for _, arch := range lr.Arches {
			dir := filepath.Join(*out, expandLayout(c.Layout.Path, r.Line, arch))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return wrap(exitIndex, err)
			}

			// A noarch package goes into every architecture's directory, because a
			// router reads exactly one index — its own — and would otherwise never
			// see it. A package built for a specific architecture goes only into
			// that one.
			//
			// Copying after signing rather than before is not incidental: ECDSA
			// signatures are randomised, so signing each copy separately would
			// produce 35 different files where subscribers should see one.
			var placed []string
			for _, src := range []string{config.Noarch, arch} {
				for _, p := range tree[src] {
					if err := copyFile(filepath.Join(dist, src, p), filepath.Join(dir, p)); err != nil {
						return wrap(exitIndex, err)
					}
					placed = append(placed, p)
				}
			}
			if len(placed) == 0 {
				continue
			}

			res, err := index.Build(ctx, tool, index.Options{
				Dir: dir, TrustDir: trustDir, Signer: signer,
				Description:      c.Feed.Title,
				UnsignedPackages: !*c.Signing.SignPackages,
			})
			if err != nil {
				return wrap(exitIndex, err)
			}
			total += len(placed)
			a.debugf("%s: %d packages, index %d bytes", dir, len(res.Packages), res.Size)

			// Badge data, read back from the index that was just written rather than
			// from the config: a badge that claims a version the feed does not serve
			// is worse than no badge, and this is the only place both are known to
			// agree. One architecture per line is enough — a package present on a
			// line appears in every architecture directory it belongs to, at the
			// same version.
			if !seenLine[r.Line] {
				idx, err := feedindex.ReadDir(dir)
				if err != nil {
					return wrap(exitIndex, err)
				}
				for _, e := range idx.Entries {
					b := badges[e.Name]
					b.Name, b.Version = e.Name, e.Version
					b.Releases = append(b.Releases, r.Line)
					badges[e.Name] = b
				}
				seenLine[r.Line] = true
			}
		}
		a.logf("%s: %d package placement(s) across %d architecture(s)", r.Line, total, len(lr.Arches))
	}

	// The public key belongs at the root of the feed, because that is the URL the
	// install snippet tells people to fetch it from.
	pub, err := keys.MarshalPublic(&key.PublicKey)
	if err != nil {
		return wrap(exitKey, err)
	}
	if err := os.WriteFile(filepath.Join(*out, c.Feed.Name+".pem"), pub, 0o644); err != nil {
		return wrap(exitIndex, err)
	}

	if err := a.writeBadges(*out, badges); err != nil {
		return err
	}
	// GitHub Pages runs Jekyll unless told not to, and Jekyll drops paths beginning
	// with an underscore or a dot. On a tree of binaries that silently removes files.
	if err := os.WriteFile(filepath.Join(*out, ".nojekyll"), nil, 0o644); err != nil {
		return wrap(exitIndex, err)
	}

	a.logf("wrote %s, signed by key %s", *out, signer.Identity)
	return nil
}

// expandLayout fills in the layout template from owfeed.yml, as a host path.
func expandLayout(path, release, arch string) string {
	return filepath.FromSlash(config.ExpandLayout(path, release, arch))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// writeBadges renders one badge file per package, so a maintainer whose work a feed
// carries can show it in their README without the feed having to know they did.
func (a *app) writeBadges(out string, badges map[string]badge.Package) error {
	list := make([]badge.Package, 0, len(badges))
	for _, b := range badges {
		list = append(list, b)
	}
	badge.Sort(list)
	if err := badge.Write(out, list); err != nil {
		return wrap(exitIndex, err)
	}
	if len(list) > 0 {
		a.debugf("wrote %d badge file(s) under %s/", len(list)*2, badge.Dir)
	}
	return nil
}
