package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"owfeed.org/owfeed/internal/apk"
	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/lock"
	"owfeed.org/owfeed/internal/usign"

	"owfeed.org/owfeed/internal/doctor"
	"owfeed.org/owfeed/internal/keys"
)

func (a *app) doctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed doctor", flag.ContinueOnError)
	fs.SetOutput(a.err)
	failOn := fs.String("fail-on", "error", "lowest severity that fails the run: warn or error")
	requireOrigin := fs.Bool("require-origin", false, "every package must say which repository it comes from")
	authorKeys := fs.String("author-keys", "", "directory of pinned author public keys; every package must be signed by one of them")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	out := defaultOut
	if fs.NArg() > 0 {
		out = fs.Arg(0)
	}

	var threshold doctor.Severity
	switch *failOn {
	case "warn":
		threshold = doctor.Warn
	case "error":
		threshold = doctor.Error
	default:
		return fail(exitConfig, "--fail-on %q is neither warn nor error", *failOn)
	}

	c, err := a.loadConfig()
	if err != nil {
		return err
	}
	l, err := a.requireLock(ctx, c)
	if err != nil {
		return err
	}
	in, err := a.doctorInput(ctx, c, l, out, *requireOrigin, *authorKeys)
	if err != nil {
		return err
	}

	report, err := doctor.Run(ctx, in)
	if err != nil {
		// A check that cannot run counts as failed. A green report that means
		// "nothing was looked at" is worse than a red one.
		return wrap(exitCheck, err)
	}

	for _, f := range report.Findings {
		fmt.Fprintln(a.out, f)
	}

	if !report.Failed(threshold) {
		a.logf("%d checks passed", report.Checked)
		if len(report.Findings) > 0 {
			a.logf("%d finding(s) below the --fail-on threshold", len(report.Findings))
		}
		return nil
	}
	return fail(exitCheck, "%d finding(s) at or above %s, out of %d checks", len(report.Findings), *failOn, report.Checked)
}

// usesFormat reports whether any release line uses a package format.
func usesFormat(c *config.Config, format string) bool {
	for _, r := range c.Releases {
		if r.Format == format {
			return true
		}
	}
	return false
}

// doctorInput assembles what the checks look at. Both `doctor` and `publish` run
// them, and building the input twice is how the ipk half of a feed came to be
// published without its index ever being verified.
func (a *app) doctorInput(ctx context.Context, c *config.Config, l *lock.Lock, out string, requireOrigin bool, authorKeyDir string) (doctor.Input, error) {
	// The apk toolchain is only needed to read an apk index. A feed that publishes
	// only for 24.10 should not have to download an SDK to be checked.
	var tool *apk.Tool
	var err error
	if usesFormat(c, config.FormatAPK) {
		if tool, err = a.tool(ctx, l); err != nil {
			return doctor.Input{}, err
		}
	}

	var usignPub *usign.PublicKey
	if c.Signing.UsignKey != "" {
		k, err := a.usignKey(c)
		if err != nil {
			return doctor.Input{}, err
		}
		if usignPub, err = usign.ParsePublicKey(k.MarshalPublic("")); err != nil {
			return doctor.Input{}, wrap(exitKey, err)
		}
	}

	// The config is the policy; the flag is for checking a tree by hand.
	if authorKeyDir == "" && c.Signing.AuthorKeys != "" {
		authorKeyDir = filepath.Join(a.root(), c.Signing.AuthorKeys)
	}
	author, err := loadAuthorKeys(authorKeyDir)
	if err != nil {
		return doctor.Input{}, err
	}

	key, err := a.signingKey(c)
	if err != nil {
		return doctor.Input{}, err
	}
	id, err := keys.IdentityOf(&key.PublicKey)
	if err != nil {
		return doctor.Input{}, wrap(exitKey, err)
	}

	return doctor.Input{
		Config:        c,
		Lock:          l,
		Tool:          tool,
		Root:          a.root(),
		OutDir:        out,
		Identity:      id,
		PubKeyName:    c.Feed.Name + ".pem",
		UsignKey:      usignPub,
		RequireOrigin: requireOrigin,
		AuthorKeys:    author,
		Excluded:      readExclusions(out),
	}, nil
}

// loadAuthorKeys reads the pinned public keys a package may be signed by.
//
// The directory is the source of truth, and it is the one thing in a feed that a
// person has to have looked at: a key added here is the whole of the decision to
// carry somebody's work. A key that arrives beside a release proves nothing by
// itself — whoever replaced the package would replace it too — so it is only ever
// compared against what is pinned here.
func loadAuthorKeys(dir string) (map[string]keys.Identity, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fail(exitConfig, "--author-keys %s: %v", dir, err)
	}
	out := map[string]keys.Identity{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pem") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, wrap(exitKey, err)
		}
		pub, err := keys.LoadPublic(b)
		if err != nil {
			return nil, fail(exitKey, "%s: %v", path, err)
		}
		id, err := keys.IdentityOf(pub)
		if err != nil {
			return nil, fail(exitKey, "%s: %v", path, err)
		}
		out[e.Name()] = id
	}
	// An empty directory is a configuration mistake rather than a feed with no
	// authors: the flag was passed, so something was expected to be in it.
	if len(out) == 0 {
		return nil, fail(exitConfig, "--author-keys %s holds no .pem files", dir)
	}
	return out, nil
}

// readExclusions reads the record `owfeed index` leaves beside the tree.
//
// Absent is normal and means nothing was excluded. Unreadable is treated the same
// way rather than reported: the file only ever downgrades a finding, so failing to
// read it makes the check stricter, never looser.
func readExclusions(out string) map[string]bool {
	b, err := os.ReadFile(filepath.Join(out, ".excluded"))
	if err != nil {
		return nil
	}
	m := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// The record holds filenames; coverage works in package names.
		if i := strings.LastIndex(line, "-"); i > 0 {
			if j := strings.LastIndex(line[:i], "-"); j > 0 {
				m[line[:j]] = true
			}
		}
		m[line] = true
	}
	return m
}
