package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/ipkindex"
	"github.com/VizzleTF/owfeed/internal/lock"
	"github.com/VizzleTF/owfeed/internal/usign"
)

// indexIPK lays out and indexes the 24.10 side of a feed.
//
// Almost nothing carries over from the apk side. opkg reads a text index, fetches
// it gzipped while the signature covers the uncompressed form, verifies usign
// rather than EC, and is pointed at a directory rather than at the index file. A
// feed serving both lines is really two feeds under one URL.
func (a *app) indexIPK(c *config.Config, l *lock.Lock, r config.Release, dist, out string) error {
	key, err := a.usignKey(c)
	if err != nil {
		return err
	}

	pkgs, err := filepath.Glob(filepath.Join(dist, "*", "*.ipk"))
	if err != nil {
		return wrap(exitIndex, err)
	}
	if len(pkgs) == 0 {
		return fail(exitIndex, "%s holds no .ipk files; run `owfeed build --format ipk` first", dist)
	}

	{
		lr, ok := l.Release(r.Line)
		if !ok {
			return fail(exitConflict, "owfeed.lock has no entry for release line %s; run `owfeed lock --update`", r.Line)
		}

		total := 0
		for _, arch := range lr.Arches {
			dir := filepath.Join(out, expandLayout(c.Layout.Path, r.Line, arch))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return wrap(exitIndex, err)
			}
			// `all` is opkg's name for what apk calls noarch, so that is the
			// directory the architecture-independent packages were built into.
			placed := 0
			for _, src := range []string{"all", arch} {
				for _, p := range pkgs {
					if filepath.Base(filepath.Dir(p)) != src {
						continue
					}
					if err := copyFile(p, filepath.Join(dir, filepath.Base(p))); err != nil {
						return wrap(exitIndex, err)
					}
					placed++
				}
			}
			if placed == 0 {
				continue
			}

			res, err := ipkindex.Build(ipkindex.Options{
				Dir: dir, Key: key,
				Comment: c.Feed.Name + " " + r.Line,
			})
			if err != nil {
				return wrap(exitIndex, err)
			}
			total += placed
			a.debugf("%s: %d packages, index %d bytes", dir, len(res.Packages), res.Size)
		}
		a.logf("%s: %d package placement(s) across %d architecture(s), opkg format", r.Line, total, len(lr.Arches))
	}

	// opkg looks a key up by its id, so the id is the filename. apk matches on the
	// identity inside the signature and ignores the name; putting the same file
	// under the wrong name breaks one manager or the other.
	if err := os.WriteFile(filepath.Join(out, key.ID.String()), key.MarshalPublic(c.Feed.Name), 0o644); err != nil {
		return wrap(exitIndex, err)
	}
	a.logf("%s: signed by usign key %s", r.Line, key.ID)
	return nil
}

func (a *app) usignKey(c *config.Config) (*usign.PrivateKey, error) {
	if c.Signing.UsignKey == "" {
		return nil, fail(exitKey, "signing.usign-key is not set; opkg verifies usign signatures and checks them by default, "+
			"so an ipk feed without one is a feed nobody can use")
	}
	scheme, arg, _ := strings.Cut(c.Signing.UsignKey, ":")
	var blob []byte
	var err error
	switch scheme {
	case "env":
		blob = []byte(os.Getenv(arg))
		if len(blob) == 0 {
			return nil, fail(exitKey, "$%s is empty; signing.usign-key reads the key from it", arg)
		}
	case "file":
		if blob, err = os.ReadFile(filepath.Join(a.root(), arg)); err != nil {
			return nil, wrap(exitKey, err)
		}
	default:
		return nil, fail(exitKey, "signing.usign-key source %q is not supported", scheme)
	}
	key, err := usign.ParsePrivateKey(blob)
	return key, wrap(exitKey, err)
}
