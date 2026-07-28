package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/keys"
	"github.com/VizzleTF/owfeed/internal/usign"
)

func (a *app) keygen(args []string) error {
	fs := flag.NewFlagSet("owfeed keygen", flag.ContinueOnError)
	fs.SetOutput(a.err)
	name := fs.String("name", "", "key name (default: the feed name from owfeed.yml)")
	out := fs.String("o", "", "where to write the private key (default: <name>.pem)")
	force := fs.Bool("force", false, "write the key even inside a git working tree")
	usignKey := fs.Bool("usign", false, "generate a usign release key instead of an apk signing key")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	// The feed name is only needed to derive a default filename, so an explicit -o
	// means there is nothing to look up.
	feedName := *name
	if feedName == "" && *out == "" {
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

	// Two different keys for two different readers. apk verifies EC signatures
	// inside a package; a release manifest is read by installers and by feeds, which
	// speak usign. Neither can stand in for the other.
	if *usignKey {
		return a.keygenUsign(abs)
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

// keygenUsign writes a usign keypair, for signing release manifests.
func (a *app) keygenUsign(path string) error {
	key, err := usign.Generate()
	if err != nil {
		return wrap(exitKey, err)
	}
	sec, err := key.MarshalPrivate("owfeed release key")
	if err != nil {
		return wrap(exitKey, err)
	}
	if err := os.WriteFile(path, sec, 0o600); err != nil {
		return wrap(exitKey, err)
	}
	pub := path[:len(path)-len(filepath.Ext(path))] + ".pub"
	if err := os.WriteFile(pub, key.MarshalPublic("owfeed release key"), 0o644); err != nil {
		return wrap(exitKey, err)
	}

	a.logf("wrote %s (private, mode 0600)", rel(path))
	a.logf("wrote %s (public — this is what a feed pins to verify your releases)", rel(pub))
	a.logf("")
	a.logf("key id: %s", key.ID)
	a.logf("")
	a.logf("This signs release manifests, not packages. A feed that carries your work pins")
	a.logf("the public half, so its signature can mean \"the author signed this\" rather than")
	a.logf("\"this downloaded successfully\".")
	return nil
}
