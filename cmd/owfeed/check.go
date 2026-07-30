package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	"owfeed.org/owfeed/internal/keys"
	"owfeed.org/owfeed/internal/usign"
)

// check is build, sign, index and doctor, on keys that exist for the length of the
// run.
//
// It is the whole of what an author's CI can verify before a tag, and every
// repository that publishes packages was hand-rolling it: four commands, two
// `keygen` calls, and the environment variables to wire them together. That block
// is identical everywhere it appears, so it belongs here.
//
// THE KEYS ARE THROWAWAY, and that is the point rather than a limitation. What this
// answers is whether the package can be built, signed and indexed, and whether the
// result survives `doctor` — none of which depends on WHOSE key signed it. A real
// key must not be reachable from this: the job that runs it is the job that runs
// pull requests, and a signing key in that scope is a key any fork can aim work at.
//
// It also removes the reason a `signing:` block appeared in repositories that
// publish no feed at all. `index` will not run without a usign key, so authors were
// declaring one — pointing at a CI variable holding nothing of consequence — purely
// to get past that check. The keys below are generated instead, and nothing has to
// be declared.
func (a *app) check(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed check", flag.ContinueOnError)
	fs.SetOutput(a.err)
	// Default on, where `doctor` defaults it off. A feed may reasonably carry a
	// package that names no upstream; an author checking their own release has no
	// excuse, and the community feeds refuse it at ingest anyway.
	requireOrigin := fs.Bool("require-origin", true, "every package must say which repository it comes from")
	failOn := fs.String("fail-on", "error", "lowest severity that fails the run: warn or error")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	cleanup, err := a.ephemeralKeys()
	if err != nil {
		return err
	}
	defer cleanup()

	// IT LEAVES NOTHING BEHIND, which is as much a part of what `check` is as the
	// keys. Everything it produces is signed by a key that will not exist in a
	// moment, and `index` adds a keyring package carrying that key's public half —
	// so a run that wrote into `dist` would leave a directory that looks like output
	// and must never be published. A later `owfeed release` in the same job would
	// happily put both in a release.
	work, err := os.MkdirTemp(tempParent(), "owfeed-check-")
	if err != nil {
		return wrap(exitInternal, err)
	}
	defer os.RemoveAll(work)
	dist := filepath.Join(work, "dist")
	out := filepath.Join(work, "out")

	for _, stage := range []struct {
		name string
		run  func() error
	}{
		{"build", func() error { return a.build(ctx, []string{"-o", dist}) }},
		{"sign", func() error { return a.sign(ctx, []string{dist}) }},
		{"index", func() error { return a.index(ctx, []string{"-o", out, dist}) }},
		{"doctor", func() error { return a.doctor(ctx, append(doctorArgs(*requireOrigin, *failOn), out)) }},
	} {
		a.logf("== %s", stage.name)
		if err := stage.run(); err != nil {
			return err
		}
	}
	return nil
}

// tempParent is RUNNER_TEMP on a GitHub runner, which is on the same volume as the
// workspace and is cleaned up with the job.
func tempParent() string {
	if p := os.Getenv("RUNNER_TEMP"); p != "" {
		return p
	}
	return os.TempDir()
}

func doctorArgs(requireOrigin bool, failOn string) []string {
	args := []string{"--fail-on", failOn}
	if requireOrigin {
		args = append(args, "--require-origin")
	}
	return args
}

// ephemeralKeys generates one of each key and points the loaded config at them.
//
// Through the environment rather than through files: these keys have no business
// existing on disk, where the next step is somebody wondering whether to commit
// them. `env:` is a key source owfeed already supports everywhere, so nothing
// downstream has to know these are different.
func (a *app) ephemeralKeys() (func(), error) {
	ec, err := keys.Generate()
	if err != nil {
		return func() {}, wrap(exitKey, err)
	}
	ecPEM, err := keys.MarshalPrivate(ec)
	if err != nil {
		return func() {}, wrap(exitKey, err)
	}

	u, err := usign.Generate()
	if err != nil {
		return func() {}, wrap(exitKey, err)
	}
	usignSec, err := u.MarshalPrivate("owfeed check, throwaway")
	if err != nil {
		return func() {}, wrap(exitKey, err)
	}

	const ecVar, usignVar = "OWFEED_CHECK_SIGN_KEY", "OWFEED_CHECK_USIGN_KEY"
	if err := os.Setenv(ecVar, string(ecPEM)); err != nil {
		return func() {}, wrap(exitInternal, err)
	}
	if err := os.Setenv(usignVar, string(usignSec)); err != nil {
		return func() {}, wrap(exitInternal, err)
	}

	a.checkKeys = &checkKeys{ec: "env:" + ecVar, usign: "env:" + usignVar}
	return func() {
		a.checkKeys = nil
		os.Unsetenv(ecVar)
		os.Unsetenv(usignVar)
	}, nil
}
