package main

import (
	"context"
	"encoding/json"
	"flag"
	"strings"

	"owfeed.org/owfeed/internal/build"
	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/ipk"
)

// plan says what a build would produce, without producing it.
//
// WHY THIS IS A COMMAND AND NOT A COMMENT IN A README. Everything it prints is
// derivable from owfeed.yml, and every repository that publishes packages derives it
// again by hand: a CI step asserting "exactly one .apk, named so a fielded updater
// picks it", a release job globbing dist/*/*.apk, a maintainer reading the config to
// work out which of two release lines a package will appear on. Each of those is a
// second implementation of this function, and they are wrong in different ways.
//
// It runs OFFLINE and BEFORE ANYTHING IS BUILT. That is the point: the questions it
// answers — will this version parse, which lines is this package on, what will the
// files be called — are the ones whose answers are expensive to discover after a tag
// exists. `owfeed check` then proves the plan can actually be carried out.
//
// The filenames come from the same functions `build` calls, never from a format
// string here: a plan that disagrees with the build is worse than no plan.
func (a *app) plan(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("owfeed plan", flag.ContinueOnError)
	fs.SetOutput(a.err)
	asJSON := fs.Bool("json", false, "print the plan as JSON, for a later job to read")
	if err := fs.Parse(args); err != nil {
		return wrap(exitConfig, err)
	}

	c, err := a.loadConfig()
	if err != nil {
		return err
	}

	p, err := a.buildPlan(c)
	if err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(a.out)
		enc.SetIndent("", "  ")
		return wrap(exitInternal, enc.Encode(p))
	}
	a.printPlan(p)
	return nil
}

// Plan is the machine-readable form. Versioned, because a later job reads it and a
// field that changes shape without saying so is a job that breaks on upgrade.
type Plan struct {
	Schema   string        `json:"schema"`
	Feed     string        `json:"feed"`
	Packages []PlanPackage `json:"packages"`
	Signing  PlanSigning   `json:"signing"`
}

type PlanPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Unresolved says why Version is empty, and is empty when it is not.
	Unresolved string         `json:"unresolved,omitempty"`
	Arch       []string       `json:"arch"`
	Depends    []string       `json:"depends,omitempty"`
	Conffiles  []string       `json:"conffiles,omitempty"`
	Artifacts  []PlanArtifact `json:"artifacts"`
}

// PlanArtifact is one file, at the path `owfeed build` will write it to.
type PlanArtifact struct {
	Line   string `json:"line"`
	Format string `json:"format"`
	Arch   string `json:"arch"`
	Path   string `json:"path"`
}

type PlanSigning struct {
	// Key is where the EC key comes from, as written in the config. Never the key.
	Key string `json:"key"`
	// Packages is false for a feed that signs only its index.
	Packages bool `json:"packages"`
	// UsignKey is the index key, empty for a repository that publishes no index.
	UsignKey string `json:"usign-key,omitempty"`
}

func (a *app) buildPlan(c *config.Config) (*Plan, error) {
	p := &Plan{
		Schema: "owfeed-plan 1",
		Feed:   c.Feed.Name,
		Signing: PlanSigning{
			Key:      c.Signing.Key,
			Packages: c.Signing.SignPackages == nil || *c.Signing.SignPackages,
			UsignKey: c.Signing.UsignKey,
		},
	}

	for _, pkg := range c.Packages {
		// Resolved rather than echoed: `version-from: file:./dist/VERSION` is the
		// common case, and the question "what version will this be" is exactly the
		// one a maintainer cannot answer by reading the config.
		//
		// AN UNRESOLVED VERSION IS NOT AN ERROR HERE, and that is the difference
		// between a plan and a build. `file:./dist/VERSION` names a file the build
		// script writes, so before staging there is nothing to read — and refusing to
		// say anything would make this command useless at the one moment it is most
		// wanted. Everything else in the plan is knowable, so it is printed, with the
		// version named as the reason it is missing. `version-from: tag` does not
		// have this problem, which is most of why it exists.
		version, verr := build.ResolveVersion(pkg, a.root())
		if verr != nil {
			version = ""
		}

		entry := PlanPackage{
			Name:       pkg.EffectiveName(),
			Version:    version,
			Unresolved: reasonFor(verr),
			Arch:       pkg.Arch.List,
			Depends:    pkg.Depends,
			Conffiles:  pkg.Conffiles,
		}

		for _, r := range c.Releases {
			if !pkg.PublishedOn(r.Line) {
				continue
			}
			// A placeholder rather than an empty string, so an unresolved version
			// reads as `…-<version>.apk` instead of `…-.apk`, which looks like a
			// filename and is not one.
			shown := version
			if shown == "" {
				shown = "<version>"
			}
			for _, arch := range pkg.Arch.List {
				entry.Artifacts = append(entry.Artifacts, planArtifact(entry.Name, shown, arch, r))
			}
		}
		p.Packages = append(p.Packages, entry)
	}
	return p, nil
}

// planArtifact mirrors what build.Build does with a name, a version and an
// architecture — including the rename opkg forces, where the architecture apk calls
// `noarch` is `all` and the directory changes with it.
func planArtifact(name, version, arch string, r config.Release) PlanArtifact {
	if r.Format == config.FormatIPK {
		pkgArch := arch
		if arch == config.Noarch {
			pkgArch = ipk.ArchAll
		}
		return PlanArtifact{
			Line: r.Line, Format: r.Format, Arch: pkgArch,
			Path: "dist/" + pkgArch + "/" + ipk.FileName(name, version, pkgArch),
		}
	}
	return PlanArtifact{
		Line: r.Line, Format: r.Format, Arch: arch,
		Path: "dist/" + arch + "/" + build.PackageFileName(name, version),
	}
}

func (a *app) printPlan(p *Plan) {
	if len(p.Packages) == 0 {
		a.logf("%s builds nothing: everything it carries is fetched already built", p.Feed)
		return
	}

	for _, pkg := range p.Packages {
		if pkg.Unresolved != "" {
			a.logf("%s (version not resolved yet)  %s", pkg.Name, strings.Join(pkg.Arch, " "))
			a.logf("  %s", pkg.Unresolved)
		} else {
			a.logf("%s %s  %s", pkg.Name, pkg.Version, strings.Join(pkg.Arch, " "))
		}
		for _, art := range pkg.Artifacts {
			a.logf("  %-6s %-4s %s", art.Line, art.Format, art.Path)
		}
		if len(pkg.Depends) > 0 {
			a.logf("  depends:   %s", strings.Join(pkg.Depends, ", "))
		}
		// Printed even when empty, because an absent conffiles: on a package that
		// ships /etc/config/* is the quietest way to lose a user's settings, and a
		// plan that stays silent about it reads as approval.
		a.logf("  conffiles: %s", orNone(pkg.Conffiles))
	}

	a.logf("")
	if !p.Signing.Packages {
		a.logf("packages are left unsigned (signing.sign-packages: false); the index carries the signature")
		return
	}
	// The asymmetry is not a limitation of owfeed and surprises everybody once: the
	// ipk container has nowhere to put a signature, so an author's signature reaches
	// 25.12 routers and not 24.10 ones. What covers the 24.10 half is the release
	// manifest, which is a different key.
	a.logf("apk packages signed from %s", p.Signing.Key)
	if hasIPK(p) {
		a.logf("ipk packages carry no in-package signature — opkg has nowhere to put one;")
		a.logf("  `owfeed release` signs them from the outside, with the usign key")
	}
}

// reasonFor turns the resolver's error into one line a person can act on. The
// resolver's own message names the path it could not read, which is the useful half.
func reasonFor(err error) string {
	if err == nil {
		return ""
	}
	return err.Error() + " — stage the tree first, or use `version-from: tag`"
}

func hasIPK(p *Plan) bool {
	for _, pkg := range p.Packages {
		for _, a := range pkg.Artifacts {
			if a.Format == config.FormatIPK {
				return true
			}
		}
	}
	return false
}

func orNone(v []string) string {
	if len(v) == 0 {
		return "(none)"
	}
	return strings.Join(v, ", ")
}
