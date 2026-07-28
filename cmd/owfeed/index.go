package main

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/VizzleTF/owfeed/internal/index"
	"github.com/VizzleTF/owfeed/internal/keys"
)

const defaultOut = "out"

func (a *app) index(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed index", flag.ContinueOnError)
	fs.SetOutput(a.err)
	out := fs.String("o", defaultOut, "directory to write the publishable tree into")
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
	tool, err := a.tool(ctx, l)
	if err != nil {
		return err
	}

	pkgs, err := index.Packages(dist)
	if err != nil {
		return wrap(exitIndex, err)
	}
	if len(pkgs) == 0 {
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

	for _, r := range c.Releases {
		lr, ok := l.Release(r.Line)
		if !ok {
			return fail(exitConflict, "owfeed.lock has no entry for release line %s; run `owfeed lock --update`", r.Line)
		}
		for _, arch := range lr.Arches {
			dir := filepath.Join(*out, expandLayout(c.Layout.Path, r.Line, arch))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return wrap(exitIndex, err)
			}
			// The same signed bytes go into every architecture directory. Copying
			// after signing rather than before is not incidental: ECDSA signatures
			// are randomised, so signing each copy separately would produce 35
			// different files where subscribers should see one.
			for _, p := range pkgs {
				if err := copyFile(filepath.Join(dist, p), filepath.Join(dir, p)); err != nil {
					return wrap(exitIndex, err)
				}
			}
			res, err := index.Build(ctx, tool, index.Options{
				Dir: dir, TrustDir: trustDir, Signer: signer,
				Description: c.Feed.Title,
			})
			if err != nil {
				return wrap(exitIndex, err)
			}
			a.debugf("%s: %d packages, index %d bytes", dir, len(res.Packages), res.Size)
		}
		a.logf("%s: %d package(s) across %d architecture(s)", r.Line, len(pkgs), len(lr.Arches))
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
	// GitHub Pages runs Jekyll unless told not to, and Jekyll drops paths beginning
	// with an underscore or a dot. On a tree of binaries that silently removes files.
	if err := os.WriteFile(filepath.Join(*out, ".nojekyll"), nil, 0o644); err != nil {
		return wrap(exitIndex, err)
	}

	a.logf("wrote %s, signed by key %s", *out, signer.Identity)
	return nil
}

// expandLayout fills in the layout template from owfeed.yml.
func expandLayout(path, release, arch string) string {
	path = strings.ReplaceAll(path, "{release}", release)
	path = strings.ReplaceAll(path, "{arch}", arch)
	return filepath.FromSlash(path)
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
