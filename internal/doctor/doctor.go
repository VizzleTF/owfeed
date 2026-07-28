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
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/VizzleTF/owfeed/internal/apk"
	"github.com/VizzleTF/owfeed/internal/config"
	"github.com/VizzleTF/owfeed/internal/index"
	"github.com/VizzleTF/owfeed/internal/keys"
	"github.com/VizzleTF/owfeed/internal/lock"
	"github.com/VizzleTF/owfeed/internal/meta"
	"github.com/VizzleTF/owfeed/internal/snippet"
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
	// RequireOrigin makes a package that does not say where it came from an error.
	// A feed carrying only its author's own work does not need this; one carrying
	// other people's does, because the URL in the installed package is the only
	// thing telling a user who to go to when it misbehaves.
	RequireOrigin bool
}

// Run executes every check.
func Run(ctx context.Context, in Input) (*Report, error) {
	r := &Report{}

	checkKeyName(r, in)
	checkDescriptions(r, in)
	checkConffileCoverage(r, in)
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

// indexEntry is one package as the index describes it.
type indexEntry struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Arch     string `json:"arch"`
	Hashes   string `json:"hashes"`
	FileSize int64  `json:"file-size"`
}

// checkTree runs everything that needs the built tree.
func checkTree(ctx context.Context, r *Report, in Input) error {
	dirs, err := indexDirs(in.OutDir)
	if err != nil {
		return err
	}
	if len(dirs) == 0 {
		return fmt.Errorf("%s holds no %s; run `owfeed index` first", in.OutDir, index.IndexFile)
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
	return nil
}

func checkIndexDir(ctx context.Context, r *Report, in Input, dir string, arches map[string]bool) error {
	where := relTo(in.OutDir, dir)

	// 401: compression. OpenWrt builds apk with -Dzstd=disabled, so a zstd index
	// parses on the build host and dies on every router.
	r.Checked++
	adb, err := os.ReadFile(filepath.Join(dir, index.IndexFile))
	if err != nil {
		return err
	}
	if magic := string(adb[:min(4, len(adb))]); magic != "ADBd" {
		r.add(Finding{
			ID: "OWF401", Severity: Error, Where: where,
			What: fmt.Sprintf("index magic is %q, not ADBd", magic),
			Why:  "OpenWrt builds apk with zstd disabled, so anything but deflate fails on the device with \"ADB compression not supported\"",
			Fix:  "rebuild the index without -C; the default compression is the correct one",
		})
	}

	// 405: index size. The wget backend ignores If-Modified-Since, so every
	// subscriber downloads the whole index on every `apk update`, forever.
	r.Checked++
	switch size := int64(len(adb)); {
	case size > 8<<20:
		r.add(Finding{
			ID: "OWF405", Severity: Error, Where: where,
			What: fmt.Sprintf("index is %.1f MB", float64(size)/(1<<20)),
			Why:  "apk's wget backend ignores If-Modified-Since, so this is downloaded in full by every subscriber on every `apk update`",
			Fix:  "split the feed, or drop versions you no longer support",
		})
	case size > 1<<20:
		r.add(Finding{
			ID: "OWF405", Severity: Warn, Where: where,
			What: fmt.Sprintf("index is %.1f MB", float64(size)/(1<<20)),
			Why:  "every subscriber downloads it in full on every `apk update`, because apk's wget backend ignores If-Modified-Since",
			Fix:  "consider splitting the feed before it grows further",
		})
	}

	// 403: the index must be signed, by the key the feed publishes.
	r.Checked++
	signers, err := index.Signatures(ctx, in.Tool, dir, index.IndexFile)
	if err != nil {
		// An index apk cannot read at all is a finding about the index, not a
		// failure of the run: reporting it alongside everything else is more use
		// than stopping here.
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
			Why:  "subscribers install the published public key; an index signed by anything else fails their `apk update`",
			Fix:  "re-run `owfeed index` with the feed's key",
		})
	}

	entries, err := readIndexJSON(dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		// 202: a version apk cannot parse has no place in the ordering, so the
		// package can never be compared or upgraded.
		r.Checked++
		if err := meta.ValidateVersion(e.Version); err != nil {
			r.add(Finding{
				ID: "OWF202", Severity: Error, Where: where + "/" + e.Name,
				What: err.Error(),
				Fix:  "rebuild the package with a version apk accepts",
			})
		}

		// 201: "all" is what OpenWrt Makefiles say and apk rejects it.
		r.Checked++
		if e.Arch != "noarch" && !arches[e.Arch] {
			r.add(Finding{
				ID: "OWF201", Severity: Error, Where: where + "/" + e.Name,
				What: fmt.Sprintf("arch is %q, which this release does not publish", e.Arch),
				Why:  "a package whose arch is not in the release's set is invisible to every device on it",
				Fix:  "use \"noarch\", or an architecture from owfeed.lock",
			})
		}

		// 404 and 402 together: the index carries no filenames, so apk derives the
		// download name from the package name and version. The file therefore has to
		// be a flat neighbour of the index under exactly that name, and it has to be
		// the file the index describes.
		r.Checked++
		file := e.Name + "-" + e.Version + ".apk"
		st, err := os.Stat(filepath.Join(dir, file))
		switch {
		case err != nil:
			r.add(Finding{
				ID: "OWF402", Severity: Error, Where: where,
				What: fmt.Sprintf("the index lists %s %s but there is no %s beside it", e.Name, e.Version, file),
				Why:  "apk builds the download URL from the package name and version relative to the index, so this entry cannot be fetched",
				Fix:  "run `owfeed index` from the directory that holds the packages",
			})
		case st.Size() != e.FileSize:
			r.add(Finding{
				ID: "OWF404", Severity: Error, Where: where + "/" + file,
				What: fmt.Sprintf("the index says %d bytes, the file is %d", e.FileSize, st.Size()),
				Why: "the package was modified after it was indexed — signing appends bytes, so this is what indexing before signing looks like; " +
					"subscribers get an integrity failure",
				Fix: "sign the packages first, then index them",
			})
		}
	}

	// 303: each package signed by the feed's key, which is what makes
	// `apk add ./file.apk` work without --allow-untrusted and therefore makes LuCI's
	// Upload Package flow usable.
	pkgs, err := index.Packages(dir)
	if err != nil {
		return err
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
		if _, statErr := os.Stat(filepath.Join(path, index.IndexFile)); statErr == nil {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

func readIndexJSON(dir string) ([]indexEntry, error) {
	b, err := os.ReadFile(filepath.Join(dir, index.JSONFile))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Packages []indexEntry `json:"packages"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, index.JSONFile), err)
	}
	return doc.Packages, nil
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
