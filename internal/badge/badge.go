// Package badge writes the JSON that shields.io renders as a README badge.
//
// A maintainer whose package a feed carries has no way to show it. The obvious
// approach — pointing shields at the feed's index.json and selecting the package by
// name — does not work: shields rejects a JSONPath filter with "query not supported",
// so `$.packages[?(@.name=='x')].version` renders an error badge rather than a
// version. Selecting by position works and is wrong, because nothing fixes the order
// of an index.
//
// So the feed publishes one small file per package instead, in shields' endpoint
// shape, and the badge URL names the package rather than querying for it. The
// numbers come from the tree that was just built, which means a badge cannot claim a
// version the feed does not actually serve.
package badge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Dir is where the badge files go, relative to the feed root.
const Dir = "badge"

// Label is the left-hand side of every badge this writes. It is deliberately the
// same on all of them: a reader scanning a README should see one name they can look
// up, not three variations of it.
const Label = "owfeed"

// colour is the accent the site uses, so a badge sits next to the documentation it
// refers to rather than looking borrowed.
const colour = "b45309"

// Endpoint is the schema shields.io expects from an endpoint badge.
//
// https://shields.io/badges/endpoint-badge — schemaVersion is the only required
// version marker, and it has been 1 since the format was introduced.
type Endpoint struct {
	SchemaVersion int    `json:"schemaVersion"`
	Label         string `json:"label"`
	Message       string `json:"message"`
	Color         string `json:"color"`
}

// Package is what a feed knows about one package after indexing it.
type Package struct {
	// Name is the package name, and becomes the badge's filename.
	Name string
	// Version is what the feed serves right now.
	Version string
	// Releases are the lines it appears on, newest first.
	Releases []string
}

// Write renders every badge for every package into <root>/badge/.
//
// Two per package. The version badge is the one worth having: it shows what the feed
// is actually serving, so a maintainer can see at a glance that an update has landed
// — or that it has not. The releases badge answers the question the version badge
// raises next, which is who can install it.
func Write(root string, pkgs []Package) error {
	if len(pkgs) == 0 {
		return nil
	}
	dir := filepath.Join(root, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, p := range pkgs {
		if p.Name == "" {
			continue
		}
		if err := write(dir, p.Name+".json", Endpoint{
			SchemaVersion: 1, Label: Label, Message: p.Version, Color: colour,
		}); err != nil {
			return err
		}
		if len(p.Releases) == 0 {
			continue
		}
		// Newest first, whatever order the caller indexed the lines in. A reader
		// checking whether their router is covered looks for their own release, and
		// the newest is the one most of them are on.
		lines := append([]string(nil), p.Releases...)
		sortReleases(lines)
		// A middle dot rather than a comma: shields renders the message verbatim, and
		// a comma reads as a list that got cut off when the badge is narrow.
		if err := write(dir, p.Name+"-releases.json", Endpoint{
			SchemaVersion: 1, Label: Label,
			Message: strings.Join(lines, " · "), Color: colour,
		}); err != nil {
			return err
		}
	}
	return nil
}

func write(dir, name string, e Endpoint) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644)
}

// URL is the badge URL a maintainer puts in their README.
func URL(feedURL, pkg, suffix string) string {
	base := strings.TrimSuffix(feedURL, "/")
	return fmt.Sprintf("https://img.shields.io/endpoint?url=%s/%s/%s%s.json", base, Dir, pkg, suffix)
}

// Sort orders packages by name so the written set does not depend on map iteration.
func Sort(pkgs []Package) {
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
}

// sortReleases orders release lines newest first.
//
// Numerically per component rather than lexically: "9.10" sorts after "25.12" as a
// string, and OpenWrt has used a leading 9 before now.
func sortReleases(lines []string) {
	sort.Slice(lines, func(i, j int) bool { return newer(lines[i], lines[j]) })
}

func newer(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, errA := strconv.Atoi(as[i])
		y, errB := strconv.Atoi(bs[i])
		if errA != nil || errB != nil {
			// Not a number on either side: fall back to something total, so the
			// order is at least stable.
			return a > b
		}
		if x != y {
			return x > y
		}
	}
	return len(as) > len(bs)
}
