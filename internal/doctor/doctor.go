// Package doctor checks a feed before its subscribers do.
//
// Every check here corresponds to a mistake that has actually shipped in a
// third-party OpenWrt feed. They are numbered so that a failure can be looked up
// and explained rather than argued with, and each one says what to do next: a check
// that only reports that something is wrong has done half the job.
//
// A check that cannot be run counts as failed, never as skipped. The alternative is
// a green report that means "nothing was looked at".
package doctor

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"owfeed.org/owfeed/internal/apk"
	"owfeed.org/owfeed/internal/config"
	"owfeed.org/owfeed/internal/feedindex"
	"owfeed.org/owfeed/internal/index"
	"owfeed.org/owfeed/internal/ipkindex"
	"owfeed.org/owfeed/internal/keyring"
	"owfeed.org/owfeed/internal/keys"
	"owfeed.org/owfeed/internal/lock"
	"owfeed.org/owfeed/internal/meta"
	"owfeed.org/owfeed/internal/snippet"
	"owfeed.org/owfeed/internal/usign"
)

// Severity orders findings.
type Severity int

const (
	Info Severity = iota
	Warn
	Error
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warn:
		return "warn"
	default:
		return "info"
	}
}

// Finding is one thing worth saying about a feed.
type Finding struct {
	ID       string
	Severity Severity
	// Where names the file or directory the finding is about.
	Where string
	// What states the problem in one sentence.
	What string
	// Why explains the consequence, which is usually the part that is not obvious.
	Why string
	// Fix is what to do about it.
	Fix string
}

func (f Finding) String() string {
	s := fmt.Sprintf("%s %s: %s", f.ID, f.Severity, f.What)
	if f.Where != "" {
		s = fmt.Sprintf("%s %s: %s: %s", f.ID, f.Severity, f.Where, f.What)
	}
	if f.Why != "" {
		s += "\n    " + f.Why
	}
	if f.Fix != "" {
		s += "\n    fix: " + f.Fix
	}
	return s
}

// Report is the outcome of a run.
type Report struct {
	Findings []Finding
	// Checked counts the checks that ran, so a clean report can say how clean.
	Checked int
}

func (r *Report) add(f Finding) { r.Findings = append(r.Findings, f) }

// Worst returns the highest severity present.
func (r *Report) Worst() Severity {
	worst := Info
	for _, f := range r.Findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	return worst
}

// Failed reports whether anything at or above failOn was found.
func (r *Report) Failed(failOn Severity) bool {
	for _, f := range r.Findings {
		if f.Severity >= failOn {
			return true
		}
	}
	return false
}

// Input is everything the checks look at.
type Input struct {
	Config *config.Config
	Lock   *lock.Lock
	Tool   *apk.Tool
	// Root is the directory the config's relative paths resolve against.
	Root string
	// OutDir is the built, publishable tree.
	OutDir string
	// Identity is the key the feed is expected to be signed by.
	Identity keys.Identity
	// PubKeyName is the file the public key is published as.
	PubKeyName string
	// UsignKey is the public half of the key an opkg index is signed with. opkg
	// verifies usign where apk verifies EC, so a feed serving 24.10 is checked
	// against a different key than the one that signs its apk side.
	UsignKey *usign.PublicKey
	// RequireOrigin makes a package that does not say where it came from an error.
	// A feed carrying only its author's own work does not need this; one carrying
	// other people's does, because the URL in the installed package is the only
	// thing telling a user who to go to when it misbehaves.
	RequireOrigin bool
	// AuthorKeys are the identities a package is allowed to be signed by, keyed by
	// the file each was read from so a finding can name it.
	//
	// Set from --author-keys, and only meaningful for a feed that carries other
	// people's work. What it buys is a claim that survives the feed: a package
	// signed by its author can be checked by anyone, at any time, against a key
	// they obtained elsewhere — where a package whose only provenance is the feed's
	// index can be checked only against the feed, and therefore proves nothing
	// about the feed itself.
	//
	// The pinned key is the source of truth. A key that travels beside a release
	// proves nothing on its own, because whoever replaced the package would replace
	// it too; its value is that it can disagree with the pin, which is how a
	// rotated or substituted signing key is noticed.
	AuthorKeys map[string]keys.Identity
	// Excluded names packages `owfeed index` deliberately left out, read from the
	// record it writes beside the tree. Without it a package excluded for want of a
	// signature is indistinguishable from one that vanished, and the check that
	// catches the second would have to be given up to tolerate the first.
	Excluded map[string]bool
}

// Run executes every check.
func Run(ctx context.Context, in Input) (*Report, error) {
	r := &Report{}

	checkKeyName(r, in)
	checkDescriptions(r, in)
	checkConffileCoverage(r, in)
	checkPayloadJSON(r, in)
	checkPayloadShell(r, in)
	checkABI(r, in)
	checkDocDrift(r, in)
	checkOrigin(r, in)

	if err := checkTree(ctx, r, in); err != nil {
		return nil, err
	}
	return r, nil
}

// 302: the published public key must not shadow OpenWrt's own.
func checkKeyName(r *Report, in Input) {
	r.Checked++
	name := in.PubKeyName
	if !strings.HasPrefix(name, "openwrt") {
		return
	}
	r.add(Finding{
		ID: "OWF302", Severity: Error, Where: name,
		What: "the published public key would be installed as " + name,
		Why: "apk scans /etc/apk/keys before /lib/apk/keys and the first file of a given name wins, " +
			"so this can shadow OpenWrt's own signing key and break verification of the official feed",
		Fix: "rename the feed so its key file does not start with \"openwrt\"",
	})
}

// MaxDescription is where LuCI's package list starts truncating.
const maxDescription = meta.MaxDescriptionBytes

// 203: a description nobody can read in the one interface most users have.
func checkDescriptions(r *Report, in Input) {
	for _, p := range in.Config.Packages {
		r.Checked++
		if len(p.Description) <= maxDescription {
			continue
		}
		r.add(Finding{
			ID: "OWF203", Severity: Warn, Where: p.EffectiveName(),
			What: fmt.Sprintf("description is %d bytes", len(p.Description)),
			Why:  fmt.Sprintf("LuCI truncates past %d bytes (luci#8561), so the tail is written for nobody", maxDescription),
			Fix:  "put the detail in the package's homepage and keep the description to a line",
		})
	}
}

// 207: every configuration file a package ships must be declared.
//
// This is the check with the quietest failure in the whole set. sysupgrade reads
// .conffiles_static to decide which files to carry across a firmware upgrade, so an
// undeclared /etc/config/foo means the user's settings are silently replaced by the
// package's defaults on every upgrade, and nothing reports it.
func checkConffileCoverage(r *Report, in Input) {
	for _, p := range in.Config.Packages {
		if p.Files == "" {
			continue
		}
		r.Checked++

		declared := map[string]bool{}
		for _, cf := range p.Conffiles {
			declared[cf] = true
		}

		root := filepath.Join(in.Root, p.Files)
		var undeclared []string
		err := filepath.WalkDir(filepath.Join(root, "etc", "config"), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// No etc/config at all is normal, and not this check's business.
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if p := "/" + filepath.ToSlash(rel); !declared[p] {
				undeclared = append(undeclared, p)
			}
			return nil
		})
		if err != nil {
			continue
		}
		if len(undeclared) == 0 {
			continue
		}
		sort.Strings(undeclared)
		r.add(Finding{
			ID: "OWF207", Severity: Error, Where: p.EffectiveName(),
			What: "ships configuration it does not declare: " + strings.Join(undeclared, ", "),
			Why: "sysupgrade reads .conffiles_static to decide what survives a firmware upgrade, " +
				"so an undeclared file is replaced by the package default on every upgrade and the user's settings are gone",
			Fix: "add them to `conffiles:` in owfeed.yml",
		})
	}
}

// 208: the ABI suffix has to be on the name and in the tag, or ImageBuilder cannot
// resolve a dependency on it.
func checkABI(r *Report, in Input) {
	for _, p := range in.Config.Packages {
		if p.ABIVersion == "" {
			continue
		}
		r.Checked++
		want := p.Name + meta.ABISuffix(p.Name, p.ABIVersion)
		if p.EffectiveName() == want {
			continue
		}
		r.add(Finding{
			ID: "OWF208", Severity: Error, Where: p.Name,
			What: fmt.Sprintf("ABI-suffixed name is %q, expected %q", p.EffectiveName(), want),
			Why:  "ImageBuilder's GetABISuffix reads the suffix off the name and matches it against the abiversion tag",
			Fix:  "leave the suffix off `name:` and let `abiversion:` add it",
		})
	}
}

// 701: the documented instructions have to be the instructions.
//
// This is a live bug in a major feed today: its README sends people to /25.12/
// while its deploy writes /openwrt-25.12/, so the documented URL 404s and has done
// for months. Nobody notices, because the person who wrote the README is the one
// person who never follows it.
//
// A README that says nothing about the feed is not a finding. One that documents
// it and disagrees with what owfeed would publish is.
func checkDocDrift(r *Report, in Input) {
	readme := filepath.Join(in.Root, "README.md")
	body, err := os.ReadFile(readme)
	if err != nil {
		return
	}
	base := strings.TrimSuffix(in.Config.Feed.URL, "/")
	if base == "" || !strings.Contains(string(body), base) {
		return
	}

	r.Checked++
	want := snippet.Shell(snippet.Input{Config: in.Config})

	var missing []string
	for _, line := range strings.Split(want, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// A line the snippet could not fill in is not a line a README should repeat.
		// A feed that only carries other people's work lists no packages, so the
		// snippet has no name to put in `apk add` -- and demanding a placeholder
		// appear verbatim would make good documentation the finding.
		if strings.Contains(line, snippet.PackagePlaceholder) {
			continue
		}
		if !strings.Contains(string(body), line) {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return
	}

	r.add(Finding{
		ID: "OWF701", Severity: Error, Where: "README.md",
		What: fmt.Sprintf("documents this feed but not the way owfeed publishes it; %d line(s) differ, starting with:\n      %s",
			len(missing), missing[0]),
		Why: "the person who wrote the instructions is the one person who never follows them, " +
			"so a documented URL that 404s can survive for months",
		Fix: "replace the block with the output of `owfeed install-snippet`",
	})
}

// 211: in a feed of other people's packages, every package says whose it is.
//
// The URL survives into the index and into what `apk info` shows on the router, so
// it is the one place a user can look to find out who publishes what they just
// installed. A feed that carries third-party work and does not record it is asking
// its subscribers to trust an anonymous list.
func checkOrigin(r *Report, in Input) {
	if !in.RequireOrigin {
		return
	}
	for _, p := range in.Config.Packages {
		r.Checked++
		if p.URL != "" || in.Config.Feed.Homepage != "" {
			continue
		}
		r.add(Finding{
			ID: "OWF211", Severity: Error, Where: p.EffectiveName(),
			What: "does not say where it comes from",
			Why: "the URL is carried into the index and shown by `apk info`, so it is the only thing that tells a user " +
				"who published what they installed",
			Fix: "set `url:` to the package's repository",
		})
	}
}

// publishesTo reports whether a package belongs in one architecture's index. A
// noarch package is fanned out to all of them; one that lists architectures goes to
// the ones it lists, and an empty list means the package is built for whatever the
// release line has.
func publishesTo(p config.Package, arch string) bool {
	if len(p.Arch.List) == 0 || p.Arch.IsNoarch() {
		return true
	}
	for _, a := range p.Arch.List {
		if a == arch {
			return true
		}
	}
	return false
}

// checkTree runs everything that needs the built tree.
func checkTree(ctx context.Context, r *Report, in Input) error {
	dirs, err := indexDirs(in.OutDir)
	if err != nil {
		return err
	}
	if len(dirs) == 0 {
		return fmt.Errorf("%s holds no index; run `owfeed index` first", in.OutDir)
	}

	arches := map[string]bool{}
	for _, rel := range in.Lock.Releases {
		for _, a := range rel.Arches {
			arches[a] = true
		}
	}

	for _, dir := range dirs {
		if err := checkIndexDir(ctx, r, in, dir, arches); err != nil {
			return err
		}
	}

	checkCoverage(r, in)
	return nil
}

// 406: everything the config publishes has to be in the tree.
//
// Every other check reads the tree and asks whether what is there is correct. None
// of them can see a package that is not there at all, so a build that half-failed —
// a fetch that died, a step that was skipped — produces a tree that passes cleanly
// and a feed that is missing packages. This happened: a tree carrying one of three
// packages on its 24.10 line passed 610 checks and was ready to publish.
//
// The config is the statement of intent, so it is the only thing that knows what
// absence looks like.
func checkCoverage(r *Report, in Input) {
	for _, rel := range in.Lock.Releases {
		for _, arch := range rel.Arches {
			// A package built from prebuilt binaries exists only for the
			// architectures its author published, and naming them in `arch:` is how
			// the config says so. Absence there is a decision, not a failure.
			var want []string
			for _, p := range in.Config.Packages {
				if p.PublishedOn(rel.Line) && publishesTo(p, arch) {
					want = append(want, p.EffectiveName())
				}
			}
			if len(want) == 0 {
				continue
			}

			r.Checked++
			where := in.Config.LayoutPath(rel.Line, arch)
			dir := filepath.Join(in.OutDir, filepath.FromSlash(where))

			idx, err := feedindex.ReadDir(dir)
			if err != nil {
				r.add(Finding{
					ID: "OWF406", Severity: Error, Where: where,
					What: fmt.Sprintf("the config publishes %d package(s) here and there is no index: %v", len(want), err),
					Why:  "subscribers point at exactly this path, so a missing directory is a feed that 404s for every router on that architecture",
					Fix:  "re-run `owfeed build` and `owfeed index`, and check the build log for a step that failed without stopping",
				})
				continue
			}

			have := map[string]bool{}
			for _, e := range idx.Entries {
				have[e.Name] = true
			}
			var missing, excluded []string
			for _, name := range want {
				if have[name] {
					continue
				}
				// Two ways to be absent, and they are not the same finding. A
				// package `owfeed index` left out for want of an author signature
				// is a decision it recorded; anything else vanished, which is what
				// this check exists to catch.
				if in.Excluded[name] {
					excluded = append(excluded, name)
					continue
				}
				missing = append(missing, name)
			}
			if len(excluded) > 0 {
				sort.Strings(excluded)
				r.add(Finding{
					ID: "OWF407", Severity: Warn, Where: where,
					What: "not carried, because no pinned author key signed them: " + strings.Join(excluded, ", "),
					Why: "the rest of the feed publishes normally — one author who has not adopted signing costs " +
						"their own package and nobody else's — but subscribers of these packages stop receiving updates " +
						"and nothing on their router says why",
					Fix: "have the author sign the release with the key pinned for them, or drop the package from the config",
				})
			}
			if len(missing) == 0 {
				continue
			}
			sort.Strings(missing)
			r.add(Finding{
				ID: "OWF406", Severity: Error, Where: where,
				What: "the config publishes packages this index does not carry: " + strings.Join(missing, ", "),
				Why: "no other check can see a package that is absent, so a build that half-failed publishes a tree " +
					"that passes everything and is missing packages",
				Fix: "check the build log for a step that failed without stopping the run, then rebuild and re-index",
			})
		}
	}
}

func checkIndexDir(ctx context.Context, r *Report, in Input, dir string, arches map[string]bool) error {
	where := relTo(in.OutDir, dir)

	idx, err := feedindex.ReadDir(dir)
	if err != nil {
		r.add(Finding{
			ID: "OWF401", Severity: Error, Where: where,
			What: "no index owfeed can read: " + err.Error(),
			Fix:  "rebuild it with `owfeed index`",
		})
		return nil
	}

	// 401: the index has to be one the device's package manager can read. For apk
	// that means deflate — OpenWrt builds it with zstd disabled, so anything else
	// parses on the build host and dies on every router. For opkg it means the
	// compressed copy subscribers download matches the one the signature covers.
	r.Checked++
	switch idx.Format {
	case feedindex.APK:
		if magic := string(idx.Raw[:min(4, len(idx.Raw))]); magic != "ADBd" {
			r.add(Finding{
				ID: "OWF401", Severity: Error, Where: where,
				What: fmt.Sprintf("index magic is %q, not ADBd", magic),
				Why:  "OpenWrt builds apk with zstd disabled, so anything but deflate fails on the device with \"ADB compression not supported\"",
				Fix:  "rebuild the index without -C; the default compression is the correct one",
			})
		}
	case feedindex.IPK:
		if err := feedindex.CheckCompressed(dir, idx); err != nil {
			r.add(Finding{
				ID: "OWF401", Severity: Error, Where: where,
				What: err.Error(),
				Why:  "opkg downloads Packages.gz and verifies the signature over Packages, so a stale compressed copy is a feed whose signature is valid and whose contents are not",
				Fix:  "rebuild the index; both files are written together",
			})
		}
	}

	// 405: index size. Neither manager revalidates cheaply — apk's wget backend
	// ignores If-Modified-Since — so this is downloaded in full by every subscriber
	// on every update, forever.
	r.Checked++
	switch {
	case idx.Size > 8<<20:
		r.add(Finding{
			ID: "OWF405", Severity: Error, Where: where,
			What: fmt.Sprintf("index is %.1f MB", float64(idx.Size)/(1<<20)),
			Why:  "every subscriber downloads it in full on every update",
			Fix:  "split the feed, or drop versions you no longer support",
		})
	case idx.Size > 1<<20:
		r.add(Finding{
			ID: "OWF405", Severity: Warn, Where: where,
			What: fmt.Sprintf("index is %.1f MB", float64(idx.Size)/(1<<20)),
			Why:  "every subscriber downloads it in full on every update",
			Fix:  "consider splitting the feed before it grows further",
		})
	}

	// 403: the index must be signed, by the key subscribers install.
	r.Checked++
	if err := checkIndexSignature(ctx, in, dir, idx, where, r); err != nil {
		return err
	}

	for _, e := range idx.Entries {
		// 202: a version the package manager cannot parse has no place in the
		// ordering, so the package can never be compared or upgraded.
		r.Checked++
		if idx.Format == feedindex.APK {
			if err := meta.ValidateVersion(e.Version); err != nil {
				r.add(Finding{
					ID: "OWF202", Severity: Error, Where: where + "/" + e.Name,
					What: err.Error(),
					Fix:  "rebuild the package with a version apk accepts",
				})
			}
		}

		// 201: an architecture the release does not publish is invisible to every
		// device on it. opkg spells the architecture-independent one "all" and apk
		// spells it "noarch"; each is correct for its own format.
		r.Checked++
		if !isAnyArch(idx.Format, e.Arch) && !arches[e.Arch] {
			r.add(Finding{
				ID: "OWF201", Severity: Error, Where: where + "/" + e.Name,
				What: fmt.Sprintf("arch is %q, which this release does not publish", e.Arch),
				Fix:  "use the architecture-independent name for this format, or one from owfeed.lock",
			})
		}

		// 402 and 404: the file the index names has to be beside it, and be the file
		// the index describes.
		r.Checked++
		path := filepath.Join(dir, e.File)
		st, statErr := os.Stat(path)
		switch {
		case statErr != nil:
			r.add(Finding{
				ID: "OWF402", Severity: Error, Where: where,
				What: fmt.Sprintf("the index lists %s %s but there is no %s beside it", e.Name, e.Version, e.File),
				Why:  "the download URL is built relative to the index, so this entry cannot be fetched",
				Fix:  "run `owfeed index` from the directory that holds the packages",
			})
			continue
		case st.Size() != e.Size:
			r.add(Finding{
				ID: "OWF404", Severity: Error, Where: where + "/" + e.File,
				What: fmt.Sprintf("the index says %d bytes, the file is %d", e.Size, st.Size()),
				Why:  "the package was modified after it was indexed; subscribers get an integrity failure",
				Fix:  "sign the packages first, then index them",
			})
			continue
		}

		// opkg records the file's own hash, which is a stronger claim than apk's
		// file size and worth checking where it exists.
		if e.SHA256 != "" {
			r.Checked++
			sum, err := feedindex.SHA256(path)
			if err != nil {
				return err
			}
			if sum != e.SHA256 {
				r.add(Finding{
					ID: "OWF404", Severity: Error, Where: where + "/" + e.File,
					What: "the file does not hash to what the index records",
					Why:  "opkg checks this before installing, so the package is unusable",
					Fix:  "rebuild the index over the packages as they are now",
				})
			}
		}
	}

	// 303: each package signed by the feed's key. apk only — opkg has no
	// per-package signature at all, and its trust rests on the index alone.
	if idx.Format != feedindex.APK {
		return nil
	}
	pkgs, err := index.Packages(dir)
	if err != nil {
		return err
	}

	// 304: signed by an author whose key this feed pinned.
	//
	// A feed that does not sign packages itself has nothing of its own inside them,
	// so this is the only thing that ties a published file to whoever built it —
	// and unlike the index, it can be checked by somebody who does not trust the
	// feed. Without it, "the author is responsible for this package" is a claim
	// with no way to test it.
	if len(in.AuthorKeys) > 0 {
		// The keyring package is the feed's own — the one package a feed publishes
		// about itself, signed by the feed because there is no third-party author to
		// sign it. Holding it to a rule about other people's work would fail every
		// publish of the only package that carries the feed's key to routers, which is
		// what indexing already exempts it from.
		var keyringPrefix string
		if in.Config != nil {
			keyringPrefix = keyring.NameFor(in.Config.Feed.Name) + "-"
		}
		for _, p := range pkgs {
			if keyringPrefix != "" && strings.HasPrefix(p, keyringPrefix) {
				continue
			}
			r.Checked++
			ids, err := index.Signatures(ctx, in.Tool, dir, p)
			if err != nil {
				return err
			}
			if matchesAny(ids, in.AuthorKeys) {
				continue
			}
			r.add(Finding{
				ID: "OWF304", Severity: Error, Where: where + "/" + p,
				What: fmt.Sprintf("carries no signature by a pinned author key (signatures: %s)", join(ids)),
				Why: "the feed's index proves only that this feed published the file. An author signature is what a " +
					"subscriber can check without trusting the feed, and what makes the author answerable for what is inside",
				Fix: "have the author run `owfeed sign` in their own CI before publishing the release, and pin the public half",
			})
		}
	}

	// A feed that deliberately leaves packages as their authors built them adds
	// nothing of its own, so 303 has nothing to find. The claim it makes is about
	// the index, which 4xx checks, and the absence of its signature inside somebody
	// else's file is the point rather than a defect.
	if in.Config != nil && in.Config.Signing.SignPackages != nil && !*in.Config.Signing.SignPackages {
		return nil
	}
	for _, p := range pkgs {
		r.Checked++
		ids, err := index.Signatures(ctx, in.Tool, dir, p)
		if err != nil {
			return err
		}
		if containsID(ids, in.Identity) {
			continue
		}
		r.add(Finding{
			ID: "OWF303", Severity: Error, Where: where + "/" + p,
			What: fmt.Sprintf("not signed by %s (signatures: %s)", in.Identity, join(ids)),
			Why: "an unsigned package needs --allow-untrusted for `apk add ./file.apk`, and LuCI's Upload Package flow cannot pass it " +
				"because package-manager-call drops arguments it does not recognise (luci#8482)",
			Fix: "run `owfeed sign` before `owfeed index`",
		})
	}
	return nil
}

// checkIndexSignature verifies the index against whichever key its format uses.
func checkIndexSignature(ctx context.Context, in Input, dir string, idx *feedindex.Index, where string, r *Report) error {
	if idx.Format == feedindex.IPK {
		if in.UsignKey == nil {
			r.add(Finding{
				ID: "OWF403", Severity: Error, Where: where,
				What: "no usign key to check this index against",
				Why:  "opkg has check_signature on by default, so an index nobody can verify is a feed nobody can use",
				Fix:  "set signing.usign-key in owfeed.yml",
			})
			return nil
		}
		if err := feedindex.VerifyUsign(dir, idx, in.UsignKey); err != nil {
			r.add(Finding{
				ID: "OWF403", Severity: Error, Where: where,
				What: "the index signature does not verify: " + err.Error(),
				Why:  "opkg checks this before reading the index, so subscribers see the feed as broken",
				Fix:  "rebuild the index with `owfeed index --format ipk`",
			})
		}
		return nil
	}

	signers, err := index.Signatures(ctx, in.Tool, dir, index.IndexFile)
	if err != nil {
		r.add(Finding{
			ID: "OWF403", Severity: Error, Where: where,
			What: "apk cannot read this index: " + err.Error(),
			Why:  "subscribers run the same code on it during `apk update`",
			Fix:  "rebuild it with `owfeed index`",
		})
		return nil
	}
	if !containsID(signers, in.Identity) {
		r.add(Finding{
			ID: "OWF403", Severity: Error, Where: where,
			What: fmt.Sprintf("index is not signed by %s (signatures: %s)", in.Identity, join(signers)),
			Why:  "subscribers install the published public key; an index signed by anything else fails their update",
			Fix:  "re-run `owfeed index` with the feed's key",
		})
	}
	return nil
}

// isAnyArch reports whether a name means "installs anywhere" in this format. The
// two managers disagree on the word: apk rejects "all" as uninstallable and opkg
// has never heard of "noarch".
func isAnyArch(format, arch string) bool {
	if format == feedindex.IPK {
		return arch == "all"
	}
	return arch == config.Noarch
}

// indexDirs finds every directory in the tree holding an index.
func indexDirs(out string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		for _, name := range []string{index.IndexFile, ipkindex.IndexFile} {
			if _, statErr := os.Stat(filepath.Join(path, name)); statErr == nil {
				dirs = append(dirs, path)
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

// matchesAny reports whether any signature on a package was made by one of the
// pinned author keys.
//
// Any, not all: apk signature blocks are additive, so a package legitimately carries
// the author's signature and possibly others. What matters is that the author's is
// among them.
func matchesAny(ids []string, allowed map[string]keys.Identity) bool {
	for _, id := range ids {
		for _, want := range allowed {
			if id == want.String() {
				return true
			}
		}
	}
	return false
}

func containsID(ids []string, want keys.Identity) bool {
	for _, id := range ids {
		if id == want.String() {
			return true
		}
	}
	return false
}

func join(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

func relTo(base, path string) string {
	r, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return r
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
