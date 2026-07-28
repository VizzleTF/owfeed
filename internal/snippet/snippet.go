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

	"github.com/VizzleTF/owfeed/internal/config"
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
	fmt.Fprintf(&b, "# Neither of those two files survives a sysupgrade on its own.\n")
	fmt.Fprintf(&b, "printf '%%s\\n' %s %s >> /etc/sysupgrade.conf\n\n", keyPath, listPath)
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
				"or use an ordinary `sysupgrade` with the `/etc/sysupgrade.conf` lines above.",
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
		pkg = "<package>"
	}
	return release, pkg
}

func expand(layout, release, arch string) string {
	return config.ExpandLayout(layout, release, arch)
}
