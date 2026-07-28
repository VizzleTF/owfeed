package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/keys"
)

func (a *app) keygen(args []string) error {
	fs := flag.NewFlagSet("owfeed keygen", flag.ContinueOnError)
	fs.SetOutput(a.err)
	name := fs.String("name", "", "key name (default: the feed name from owfeed.yml)")
	out := fs.String("o", "", "where to write the private key (default: <name>.pem)")
	force := fs.Bool("force", false, "write the key even inside a git working tree")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	feedName := *name
	if feedName == "" {
		c, err := config.Load(a.configPath)
		if err != nil {
			return fail(exitConfig, "no --name given and no usable %s to take the feed name from", a.configPath)
		}
		feedName = c.Feed.Name
	}

	path := *out
	if path == "" {
		path = feedName + ".pem"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return wrap(exitInternal, err)
	}

	// A signing key inside a checkout is one `git add -A` away from being published,
	// and the mistake is unrecoverable: apk has no revocation, no expiry and no
	// signal that a key is dead.
	if !*force {
		if root, ok := gitRoot(filepath.Dir(abs)); ok {
			return fail(exitKey, "%s is inside the git working tree at %s\n"+
				"  a published signing key cannot be revoked: apk has no CRL, no expiry and no way to say a key is dead, "+
				"so a key that reaches a commit has to be replaced on every subscriber by hand\n"+
				"  write it outside the tree, or pass --force if you are certain it is ignored", path, root)
		}
	}

	key, err := keys.Generate()
	if err != nil {
		return wrap(exitKey, err)
	}
	if err := keys.WritePrivate(abs, key); err != nil {
		return wrap(exitKey, err)
	}
	id, err := keys.IdentityOf(&key.PublicKey)
	if err != nil {
		return wrap(exitKey, err)
	}
	pub, err := keys.MarshalPublic(&key.PublicKey)
	if err != nil {
		return wrap(exitKey, err)
	}
	pubPath := abs[:len(abs)-len(filepath.Ext(abs))] + ".pub.pem"
	if err := os.WriteFile(pubPath, pub, 0o644); err != nil {
		return wrap(exitKey, err)
	}

	a.logf("wrote %s (private, mode 0600)", path)
	a.logf("wrote %s (public — this is what subscribers install)", rel(pubPath))
	a.logf("")
	a.logf("key identity: %s", id)
	a.logf("  apk matches keys by this identity, not by filename, so several keys can")
	a.logf("  coexist on a device and a rotation costs nothing.")
	a.logf("")
	a.logf("Put the private key in CI and keep it nowhere else:")
	a.logf("  gh secret set %s < %s", config.DefaultKeyEnv, path)
	return nil
}

// gitRoot walks up looking for a .git entry.
func gitRoot(dir string) (string, bool) {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func rel(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	r, err := filepath.Rel(wd, path)
	if err != nil {
		return path
	}
	return r
}
