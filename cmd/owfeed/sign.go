package main

import (
	"context"
	"crypto/ecdsa"
	"flag"
	"os"

	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/index"
	"github.com/VizzleTF/owfeed/internal/keys"
)

func (a *app) sign(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed sign", flag.ContinueOnError)
	fs.SetOutput(a.err)
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	dir := defaultDist
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
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

	key, err := a.signingKey(c)
	if err != nil {
		return err
	}
	keyDir, cleanup, err := a.stageKey(key)
	if err != nil {
		return err
	}
	defer cleanup()

	signer, err := index.NewSigner(keyDir, keyFileName, key)
	if err != nil {
		return wrap(exitKey, err)
	}

	signed, err := index.Sign(ctx, tool, dir, signer)
	if err != nil {
		return wrap(exitKey, err)
	}
	for _, p := range signed {
		a.logf("signed %s", p)
	}
	// Signing each package is what makes `apk add ./file.apk` work without a flag,
	// and what makes LuCI's Upload Package flow usable at all: package-manager-call
	// drops arguments it does not recognise, so it cannot pass --allow-untrusted.
	a.logf("%d package(s) signed by key %s", len(signed), signer.Identity)
	return nil
}

// keyFileName is what the private key is called inside its staging directory. The
// name is arbitrary: apk matches keys by identity, never by filename.
const keyFileName = "signing.pem"

func (a *app) signingKey(c *config.Config) (*ecdsa.PrivateKey, error) {
	key, err := keys.Source(c.Signing.Key).Load(a.root())
	if err != nil {
		return nil, wrap(exitKey, err)
	}
	return key, nil
}

// stageKey materialises the private key in a directory of its own, outside anything
// that will be published or uploaded, and returns a function that removes it.
func (a *app) stageKey(key *ecdsa.PrivateKey) (string, func(), error) {
	parent := os.Getenv("RUNNER_TEMP")
	if parent == "" {
		parent = os.TempDir()
	}
	dir, err := keys.StageDir(parent, keyFileName, key)
	if err != nil {
		return "", func() {}, wrap(exitKey, err)
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}
