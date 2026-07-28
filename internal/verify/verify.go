// Package verify checks a published feed from outside, over its documented URL.
//
// Everything else looks at a directory on disk. This looks at what subscribers
// actually reach, which is a different thing: a tree can be perfect and still be
// served behind a redirect apk will not follow, or through a cache that hands out
// yesterday's package with today's index.
//
// It also compares what is about to be published against what is already live,
// which is the one check that catches a version republished with different
// contents. apk identifies a package by the hash the index records, so two
// payloads under one version are two packages claiming to be the same one — and
// the maintainer, whose local tree is self-consistent, sees nothing wrong.
package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Options configure a run.
type Options struct {
	// BaseURL is the feed's documented address, exactly as the install snippet
	// gives it.
	BaseURL string
	// FeedName seeds the published key's filename.
	FeedName string
	// Release is the release line.
	Release string
	// LayoutPath is the layout template.
	LayoutPath string
	// Arch is the architecture whose index is fetched.
	Arch string
	// LocalDir is the tree about to be published. When set, every package it holds
	// that is already live is compared against the published one.
	LocalDir string
}

// Finding is one problem with the published feed.
type Finding struct {
	ID   string
	What string
	Why  string
	Fix  string
}

func (f Finding) String() string {
	s := f.ID + ": " + f.What
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
	Checked  int
	// Compared counts packages that exist both live and locally.
	Compared int
	// Notes are things worth saying that are not findings.
	Notes []string
}

func (r *Report) add(f Finding) { r.Findings = append(r.Findings, f) }

// noRedirect turns every redirect into a response we can inspect, because a
// redirect is the finding rather than something to follow.
func client() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Run checks the published feed.
func Run(ctx context.Context, opts Options) (*Report, error) {
	r := &Report{}
	hc := client()
	base := strings.TrimSuffix(opts.BaseURL, "/")
	repo := base + "/" + expandLayout(opts.LayoutPath, opts.Release, opts.Arch)

	// 510: apk does not follow 30x with the stock uclient-fetch (openwrt#17180), so
	// a redirect anywhere on these URLs is a feed that works in a browser and not on
	// a router.
	for _, u := range []string{
		base + "/" + opts.FeedName + ".pem",
		repo + "/packages.adb",
	} {
		r.Checked++
		resp, err := hc.Get(u)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", u, err)
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()

		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			r.add(Finding{
				ID:   "OWF510",
				What: fmt.Sprintf("%s answers %s, redirecting to %s", u, resp.Status, resp.Header.Get("Location")),
				Why:  "apk does not follow redirects with the stock uclient-fetch (openwrt#17180), so this URL works in a browser and fails on a router",
				Fix:  "publish at the address the install snippet gives, without an apex-to-www or http-to-https hop",
			})
		case resp.StatusCode != http.StatusOK:
			r.add(Finding{
				ID:   "OWF510",
				What: fmt.Sprintf("%s answers %s", u, resp.Status),
				Fix:  "the install snippet points here, so this has to be fetchable",
			})
		}
	}

	entries, err := liveIndex(ctx, hc, repo)
	if err != nil {
		return nil, err
	}

	var local map[string]string
	if opts.LocalDir != "" {
		if local, err = localHashes(opts.LocalDir, opts.LayoutPath, opts.Release, opts.Arch); err != nil {
			return nil, err
		}
	}

	for _, e := range entries {
		name := fmt.Sprintf("%s-%s.apk", e.Name, e.Version)
		u := repo + "/" + name

		// 512: the index and the packages it names are cached independently by every
		// CDN, so a package that is missing or a different size than the index
		// records is cache skew — which reaches subscribers as an integrity failure.
		r.Checked++
		body, status, err := get(ctx, hc, u)
		switch {
		case err != nil:
			return nil, err
		case status != http.StatusOK:
			r.add(Finding{
				ID:   "OWF512",
				What: fmt.Sprintf("the index lists %s but %s answers %d", name, u, status),
				Why:  "the index and the packages it names are cached independently, so subscribers see this as a broken download",
				Fix:  "republish, and check the cache headers on the index",
			})
			continue
		case int64(len(body)) != e.FileSize:
			r.add(Finding{
				ID:   "OWF512",
				What: fmt.Sprintf("%s is %d bytes, the live index says %d", name, len(body), e.FileSize),
				Why:  "apk checks the package against the size and hash the index recorded, so this fails on every device",
				Fix:  "republish packages first and the index last, so the index is never ahead of what it describes",
			})
			continue
		}

		// 513: a version that is already published must keep its content.
		//
		// The comparison is the payload identity apk records as `hashes`, not the
		// file's bytes. Those differ on every run for a reason that is not a change:
		// ECDSA signatures are randomised, so signing the same package twice produces
		// two different files with identical contents. Comparing bytes would report
		// every republication as tampering and teach people to ignore the check.
		if local == nil {
			continue
		}
		lh, ok := local[e.Name+" "+e.Version]
		if !ok {
			continue
		}

		r.Checked++
		r.Compared++
		if lh != e.Hashes {
			r.add(Finding{
				ID:   "OWF513",
				What: fmt.Sprintf("%s is already published with different content", name),
				Why: "apk identifies a package by this hash, so two different payloads under one version are two packages claiming to be the same one; " +
					"the local tree is self-consistent, which is why nothing else notices",
				Fix: "bump the revision — the -r<n> on the version — rather than replacing what is already out there",
			})
		}
	}

	// Re-signing changes a package's bytes without changing what it contains, so
	// every republication invalidates whatever a CDN has cached for the packages
	// that did not change. Worth saying once, not per package.
	if r.Compared > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf(
			"%d package(s) are being republished unchanged; their bytes differ because ECDSA signatures are randomised, "+
				"so any cached copy a CDN holds no longer matches the new index", r.Compared))
	}
	return r, nil
}

// localHashes reads the payload identity of each package from the index owfeed
// just wrote, so no apk toolchain is needed to make the comparison.
func localHashes(dir, layout, release, arch string) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, expandLayout(layout, release, arch), "index.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Packages []indexEntry `json:"packages"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range doc.Packages {
		out[p.Name+" "+p.Version] = p.Hashes
	}
	return out, nil
}

type indexEntry struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Hashes   string `json:"hashes"`
	FileSize int64  `json:"file-size"`
}

func liveIndex(ctx context.Context, hc *http.Client, repo string) ([]indexEntry, error) {
	body, status, err := get(ctx, hc, repo+"/index.json")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET %s/index.json: %d", repo, status)
	}
	var doc struct {
		Packages []indexEntry `json:"packages"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s/index.json: %w", repo, err)
	}
	return doc.Packages, nil
}

func get(ctx context.Context, hc *http.Client, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return body, resp.StatusCode, err
}

func expandLayout(path, release, arch string) string {
	if path == "" {
		path = "releases/{release}/{arch}"
	}
	path = strings.ReplaceAll(path, "{release}", release)
	return strings.ReplaceAll(path, "{arch}", arch)
}
