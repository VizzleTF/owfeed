package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"owfeed.org/owfeed/internal/apk"

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
	// The pinned author keys, when the config names them. Empty means the feed makes
	// no claim about who built what, and every package is placed.
	authorKeys, err := loadAuthorKeys(authorKeyDirFor(a, c))
	if err != nil {
		return err
	}

	// Packages excluded for want of an author signature, so the summary can name how
	// many rather than leaving it to whoever reads 35 architectures of output.
	skipped := map[string]bool{}

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
					// A package nobody signed is left out rather than failing the
					// whole tree. One author who has not adopted signing yet should
					// cost their own package and nothing else — a feed carrying
					// several people's work cannot make everyone wait for the
					// slowest of them.
					//
					// Left out LOUDLY. A package that quietly disappears is the
					// failure this project has already had: every check reads the
					// tree, so absence looks identical to "there was nothing to
					// publish", and a feed once shipped one package of three and
					// reported itself healthy. Hence the name on stdout and the
					// count in the summary, not a debug line.
					if len(authorKeys) > 0 {
						ok, err := signedByAuthor(ctx, tool, filepath.Join(dist, src), p, authorKeys)
						if err != nil {
							return wrap(exitIndex, err)
						}
						if !ok {
							if !skipped[p] {
								a.logf("EXCLUDED %s: no signature by a pinned author key", p)
								skipped[p] = true
							}
							continue
						}
					}
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

	// A record of what was left out, beside the tree rather than only in a log that
	// scrolls away. It is what lets `doctor` tell a package excluded on purpose from
	// one that vanished because a build half-failed — the second is an error and
	// always has been, and blurring the two would give up the check that exists
	// because this feed once published one package of three.
	if err := writeExclusions(*out, skipped); err != nil {
		return wrap(exitIndex, err)
	}

	// Named, not merely counted: a maintainer reading CI output has to be able to see
	// WHICH package stopped being published, and act on it.
	if len(skipped) > 0 {
		names := make([]string, 0, len(skipped))
		for n := range skipped {
			names = append(names, n)
		}
		sort.Strings(names)
		a.logf("%d package(s) excluded for want of an author signature: %s",
			len(names), strings.Join(names, ", "))
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

// authorKeyDirFor resolves signing.author-keys against the config's directory.
func authorKeyDirFor(a *app, c *config.Config) string {
	if c.Signing.AuthorKeys == "" {
		return ""
	}
	return filepath.Join(a.root(), c.Signing.AuthorKeys)
}

// signedByAuthor reports whether a package carries a signature by one of the pinned
// keys.
//
// Read from the built artifact rather than taken on trust from whoever staged it:
// this is the one place the feed can still tell the difference, and after indexing it
// is too late.
func signedByAuthor(ctx context.Context, tool *apk.Tool, dir, pkg string, allowed map[string]keys.Identity) (bool, error) {
	ids, err := index.Signatures(ctx, tool, dir, pkg)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		for _, want := range allowed {
			if id == want.String() {
				return true, nil
			}
		}
	}
	return false, nil
}

// ExclusionsFile records packages left out of the index, and why.
const exclusionsFile = ".excluded"

// writeExclusions leaves the record beside the tree. It is rewritten every run,
// including to empty, so a package that starts being signed stops being listed.
func writeExclusions(out string, skipped map[string]bool) error {
	path := filepath.Join(out, exclusionsFile)
	if len(skipped) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	names := make([]string, 0, len(skipped))
	for n := range skipped {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Packages left out of this index for want of a signature by a pinned\n")
	b.WriteString("# author key. Written by `owfeed index`; read by `owfeed doctor`.\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s\n", n)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
