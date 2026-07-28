package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/VizzleTF/owfeed/internal/apk"
	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/usign"

	"github.com/VizzleTF/owfeed/internal/doctor"
	"github.com/VizzleTF/owfeed/internal/keys"
)

func (a *app) doctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed doctor", flag.ContinueOnError)
	fs.SetOutput(a.err)
	failOn := fs.String("fail-on", "error", "lowest severity that fails the run: warn or error")
	requireOrigin := fs.Bool("require-origin", false, "every package must say which repository it comes from")
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
	// The apk toolchain is only needed to read an apk index. A feed that publishes
	// only for 24.10 should not have to download an SDK to be checked.
	var tool *apk.Tool
	if usesFormat(c, config.FormatAPK) {
		if tool, err = a.tool(ctx, l); err != nil {
			return err
		}
	}

	var usignPub *usign.PublicKey
	if c.Signing.UsignKey != "" {
		k, err := a.usignKey(c)
		if err != nil {
			return err
		}
		if usignPub, err = usign.ParsePublicKey(k.MarshalPublic("")); err != nil {
			return wrap(exitKey, err)
		}
	}

	key, err := a.signingKey(c)
	if err != nil {
		return err
	}
	id, err := keys.IdentityOf(&key.PublicKey)
	if err != nil {
		return wrap(exitKey, err)
	}

	report, err := doctor.Run(ctx, doctor.Input{
		Config:        c,
		Lock:          l,
		Tool:          tool,
		Root:          a.root(),
		OutDir:        out,
		Identity:      id,
		PubKeyName:    c.Feed.Name + ".pem",
		UsignKey:      usignPub,
		RequireOrigin: *requireOrigin,
	})
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
