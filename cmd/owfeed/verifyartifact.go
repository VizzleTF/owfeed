package main

import (
	"encoding/hex"
	"flag"
	"os"

	"owfeed.org/owfeed/internal/usign"
)

// verifyArtifact checks an upstream release against its author's signature.
//
// A feed that ingests other people's packages signs them with its own key, because
// that is the only key its subscribers install: trust in apk flows from the signed
// index, and only the feed can sign an index describing everything the feed
// carries. What the feed's signature is worth therefore depends entirely on what it
// checked before applying it.
//
// A sha256 pin recorded in the feed's repository proves the bytes did not change
// since a maintainer looked at them. It says nothing about who produced them. A
// detached signature from the author's key does, and that is a different and
// stronger claim — the one that makes it defensible for an update to merge itself.
func (a *app) verifyArtifact(args []string) error {
	fs := flag.NewFlagSet("owfeed verify-artifact", flag.ContinueOnError)
	fs.SetOutput(a.err)
	keyPath := fs.String("key", "", "the author's public key, in usign form")
	sigPath := fs.String("signature", "", "detached signature (default: <file>.sig)")
	wantID := fs.String("key-id", "", "require this key id, as a further pin on which key is accepted")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}
	if fs.NArg() != 1 {
		return fail(exitConfig, "usage: owfeed verify-artifact --key KEY [--signature SIG] FILE")
	}
	file := fs.Arg(0)
	if *keyPath == "" {
		return fail(exitConfig, "--key is required: a signature with no pinned key verifies nothing, "+
			"since whoever replaced the artifact can replace the signature beside it")
	}
	sig := *sigPath
	if sig == "" {
		sig = file + ".sig"
	}

	keyBytes, err := os.ReadFile(*keyPath)
	if err != nil {
		return wrap(exitConfig, err)
	}
	pub, err := usign.ParsePublicKey(keyBytes)
	if err != nil {
		return wrap(exitConfig, err)
	}
	sigBytes, err := os.ReadFile(sig)
	if err != nil {
		return wrap(exitCheck, err)
	}
	parsed, err := usign.ParseSignature(sigBytes)
	if err != nil {
		return wrap(exitCheck, err)
	}
	message, err := os.ReadFile(file)
	if err != nil {
		return wrap(exitCheck, err)
	}

	// The key id inside a signature says which key to look for, never that the key
	// is the right one: whoever can replace the signature writes the id in it. A
	// caller who knows which key it expects can pin that too.
	if *wantID != "" {
		want, err := hex.DecodeString(*wantID)
		if err != nil || len(want) != len(parsed.ID) {
			return fail(exitConfig, "--key-id %q is not a 16-hex-digit usign key id", *wantID)
		}
		if parsed.ID.String() != *wantID {
			return fail(exitCheck, "%s is signed by key %s, and %s was required", file, parsed.ID, *wantID)
		}
	}

	if err := usign.Verify(pub, parsed, message); err != nil {
		return wrap(exitCheck, err)
	}
	a.logf("%s: signature by key %s verifies", file, parsed.ID)
	return nil
}
