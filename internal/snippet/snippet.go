// Package snippet renders the instructions a feed's subscribers follow.
//
// It exists as one function with one output so that the README, the generated
// index page and the maintainer's memory cannot drift apart. That drift is not
// hypothetical: a large feed currently documents /25.12/ while its deploy writes
// /openwrt-25.12/, so the documented URL 404s.
package snippet

import (
	"fmt"
	"strings"

	"owfeed.org/owfeed/internal/config"
)

// Input is what the instructions are rendered from.
type Input struct {
	Config *config.Config
	// UsignKeyID names the published opkg key, whose filename is its id.
	UsignKeyID string
	// Release is the line to advertise. Empty means the config's default.
	Release string
	// Package is the package used in the example. Empty means the first configured.
	Package string
}

// Shell renders the instructions as commands to paste into a router shell.
//
// 24.10 and earlier get a different set, because opkg and apk agree on almost
// nothing: the repository line names a directory rather than the index file, the
// key is installed under its own id as a filename rather than any name, and the
// two verify different signature schemes. A snippet that mixed them would look
// right and work on neither.
func Shell(in Input) string {
	if release, _ := resolve(in); in.Format(release) == "ipk" {
		return shellOpkg(in)
	}
	return shellAPK(in)
}

// shellOpkg renders the 24.10 instructions.
func shellOpkg(in Input) string {
	f := in.Config.Feed
	release, pkg := resolve(in)
	base := strings.TrimSuffix(f.URL, "/")

	// opkg is pointed at the directory and appends Packages.gz itself, and it reads
	// the architecture from a file the image ships.
	repo := fmt.Sprintf("%s/%s", base, expand(in.Config.Layout.Path, release, "$(. /etc/openwrt_release; echo $DISTRIB_ARCH)"))

	var b strings.Builder
	fmt.Fprintf(&b, "# The key file's NAME is its id — opkg looks it up by that.\n")
	fmt.Fprintf(&b, "wget %s/%s -O /etc/opkg/keys/%s\n\n", base, in.UsignKeyID, in.UsignKeyID)
	fmt.Fprintf(&b, "echo \"src/gz %s %s\" >> /etc/opkg/customfeeds.conf\n\n", f.Name, repo)
	fmt.Fprintf(&b, "opkg update && opkg install %s\n", pkg)
	return b.String()
}

func shellAPK(in Input) string {
	f := in.Config.Feed
	release, pkg := resolve(in)
	base := strings.TrimSuffix(f.URL, "/")
	keyPath := "/etc/apk/keys/" + f.Name + ".pem"
	listPath := "/etc/apk/repositories.d/" + f.Name + ".list"

	// The architecture comes from /etc/apk/arch rather than `apk --print-arch`:
	// the latter reports the value apk was compiled with, which is not always the
	// one the image actually uses.
	repoLine := fmt.Sprintf("%s/%s/packages.adb", base,
		expand(in.Config.Layout.Path, release, "$(cat /etc/apk/arch)"))

	var b strings.Builder
	fmt.Fprintf(&b, "# HTTPS on a stock image needs these two first.\n")
	fmt.Fprintf(&b, "apk add ca-bundle libustream-mbedtls\n\n")
	fmt.Fprintf(&b, "wget %s/%s.pem -O %s\n", base, f.Name, keyPath)
	fmt.Fprintf(&b, "echo \"%s\" > %s\n\n", repoLine, listPath)
	// Neither file survives sysupgrade on its own, and a missing key is the most
	// common cause of UNTRUSTED signature after a firmware upgrade.
	//
	// keep.d rather than /etc/sysupgrade.conf. sysupgrade reads both --
	// list_static_conffiles feeds `find` from /etc/sysupgrade.conf and
	// /lib/upgrade/keep.d/* together -- but this one is a whole file rather than
	// lines appended to somebody else's. Re-running the install rewrites it instead
	// of adding a second copy of both paths, which `>>` does every time, and
	// removing the feed is `rm` rather than editing a config by hand.
	//
	// Verified on 25.12.5: with the keep.d file present `sysupgrade
	// --create-backup` contains both paths; without it, neither.
	fmt.Fprintf(&b, "# Keep the key and the repository across a firmware upgrade.\n")
	fmt.Fprintf(&b, "mkdir -p /lib/upgrade/keep.d\n")
	fmt.Fprintf(&b, "printf '%%s\\n' %s %s > /lib/upgrade/keep.d/%s\n\n", keyPath, listPath, f.Name)
	fmt.Fprintf(&b, "apk update && apk add %s\n", pkg)
	return b.String()
}

// Markdown renders the block a README carries.
func Markdown(in Input) string {
	f := in.Config.Feed
	release, _ := resolve(in)

	var b strings.Builder
	title := f.Title
	if title == "" {
		title = f.Name
	}
	fmt.Fprintf(&b, "## Installing %s\n\n", title)
	fmt.Fprintf(&b, "OpenWrt %s and later, any architecture.\n\n", release)
	fmt.Fprintf(&b, "```sh\n%s```\n\n", Shell(in))

	for _, w := range Warnings(in) {
		fmt.Fprintf(&b, "> **%s** %s\n\n", w.Title, w.Body)
	}
	return b.String()
}

// Warning is something a subscriber has to be told, whether or not it is welcome.
type Warning struct {
	Title string
	Body  string
}

// Warnings are rendered every time and are not configurable. Each one describes a
// behaviour that surprises people after they have already installed the feed.
func Warnings(in Input) []Warning {
	_, pkg := resolve(in)
	return []Warning{
		{
			Title: "Do not install the .apk file directly.",
			Body: "`apk add ./" + pkg + "-*.apk` writes a pin on the package's content hash into `/etc/apk/world`, " +
				"and that file survives sysupgrade. The package would then never be upgraded from this feed again. " +
				"Add the repository and install by name.",
		},
		{
			Title: "Attended Sysupgrade will not carry these packages across.",
			Body: "`owut` forwards no custom repositories, and the sysupgrade server's `repository_allow_list` is empty by default, " +
				"which denies everything. Either exclude these packages from the `owut` run and reinstall them afterwards, " +
				"or use an ordinary `sysupgrade`, which keeps the key and the repository through the `/lib/upgrade/keep.d` entry above.",
		},
		{
			Title: "Installing the key trusts this feed for every package name.",
			Body: "A key in `/etc/apk/keys` validates an index claiming any package name at all, so this feed could offer a higher version of " +
				"a base package and win. Install it because you trust whoever publishes it, not because a page told you to.",
		},
	}
}

// Format reports the package format a release line uses.
func (in Input) Format(line string) string {
	for _, r := range in.Config.Releases {
		if r.Line == line {
			return r.Format
		}
	}
	return config.FormatAPK
}

func resolve(in Input) (release, pkg string) {
	release = in.Release
	if release == "" {
		release = in.Config.DefaultRelease().Line
	}
	pkg = in.Package
	if pkg == "" && len(in.Config.Packages) > 0 {
		pkg = in.Config.Packages[0].EffectiveName()
	}
	if pkg == "" {
		// A feed that only carries other people's work lists no packages, so there
		// is no name to suggest. Placeholder, and callers that compare a README
		// against this must not expect it verbatim.
		pkg = PackagePlaceholder
	}
	return release, pkg
}

// PackagePlaceholder stands in for a package name the config does not know.
const PackagePlaceholder = "<package>"

func expand(layout, release, arch string) string {
	return config.ExpandLayout(layout, release, arch)
}
