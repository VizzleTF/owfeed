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
	// The key is the file that does not survive: customfeeds.conf is a conffile and
	// is kept, /etc/opkg/keys/<id> is not shipped by any package and is not. Losing
	// only the key leaves a feed configured and unverifiable, which reads as the feed
	// being broken.
	fmt.Fprintf(&b, "# Keep the key and the repository across a firmware upgrade.\n")
	fmt.Fprintf(&b, "mkdir -p /lib/upgrade/keep.d\n")
	fmt.Fprintf(&b, "printf '%%s\\n' /etc/opkg/keys/%s /etc/opkg/customfeeds.conf > /lib/upgrade/keep.d/%s\n\n",
		in.UsignKeyID, f.Name)
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

// ScriptName is the file the subscribe script is published as, at the feed root.
const ScriptName = "subscribe.sh"

// Script renders one POSIX script that subscribes a router to this feed on
// either release line.
//
// The two lines need different files in different places, and a person pasting
// commands has to know which of the two they are on before they start. The
// script asks the router instead: apk exists or it does not. That is the whole
// reason this exists next to Shell, which stays as the thing you read to see
// what is being done to your router.
//
// It refuses a release line the feed does not publish for rather than guessing.
// A guessed URL 404s at `apk update`, which reads as "the feed is broken" and
// sends the report to the wrong person.
func Script(in Input) string {
	f := in.Config.Feed
	base := strings.TrimSuffix(f.URL, "/")
	title := f.Title
	if title == "" {
		title = f.Name
	}

	var apkLines, ipkLines []string
	for _, r := range in.Config.Releases {
		if r.Format == config.FormatIPK {
			ipkLines = append(ipkLines, r.Line)
			continue
		}
		apkLines = append(apkLines, r.Line)
	}

	keyPath := "/etc/apk/keys/" + f.Name + ".pem"
	listPath := "/etc/apk/repositories.d/" + f.Name + ".list"
	apkRepo := base + "/" + expand(in.Config.Layout.Path, `$line`, `$arch`) + "/packages.adb"
	ipkRepo := base + "/" + expand(in.Config.Layout.Path, `$line`, `$arch`)

	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	p("#!/bin/sh")
	p("# Subscribe this router to %s.", title)
	p("#")
	p("# Installing the key trusts %s for every package name, not only its own:", f.Name)
	p("# apk and opkg validate an index by its signature, and an index may claim any")
	p("# name at all. Run this because you trust who publishes it.")
	p("#")
	p("# Generated by owfeed. Re-fetch it rather than editing a copy.")
	p("set -eu")
	p("")
	p("[ \"$(id -u)\" = 0 ] || { echo \"run this as root\" >&2; exit 1; }")
	p("")
	p("# DISTRIB_RELEASE is the point release (25.12.5); the feed is laid out by line.")
	p(". /etc/openwrt_release")
	p("line=$(echo \"${DISTRIB_RELEASE:-}\" | cut -d. -f1,2)")
	p("")
	p("unsupported() {")
	p("\techo \"%s publishes for: $1\" >&2", f.Name)
	p("\techo \"this router reports ${DISTRIB_RELEASE:-an unknown release} ($2)\" >&2")
	p("\techo \"see %s\" >&2", "https://owfeed.org/install/")
	p("\texit 1")
	p("}")
	p("")
	p("if command -v apk >/dev/null 2>&1; then")
	p("\tcase \" %s \" in *\" $line \"*) ;; *) unsupported \"%s\" apk ;; esac",
		strings.Join(apkLines, " "), strings.Join(apkLines, ", "))
	p("")
	p("\t# /etc/apk/arch, not `apk --print-arch`: the latter reports what apk was")
	p("\t# compiled with, which is not always what the image uses.")
	p("\tarch=$(cat /etc/apk/arch)")
	p("")
	p("\t# A stock image cannot fetch over HTTPS until these are installed.")
	p("\tapk add ca-bundle libustream-mbedtls")
	p("\twget -qO %s %s/%s.pem", keyPath, base, f.Name)
	p("\techo \"%s\" > %s", apkRepo, listPath)
	p("")
	p("\t# Neither file survives sysupgrade on its own, and a missing key after a")
	p("\t# firmware upgrade reads as UNTRUSTED signature. A whole keep.d file rather")
	p("\t# than lines appended to /etc/sysupgrade.conf, so re-running this replaces")
	p("\t# it instead of adding a second copy.")
	p("\tmkdir -p /lib/upgrade/keep.d")
	p("\tprintf '%%s\\n' %s %s > /lib/upgrade/keep.d/%s", keyPath, listPath, f.Name)
	p("")
	p("\tapk update")
	p("\techo \"%s added. Install by name: apk add <package>\"", title)
	p("else")
	if len(ipkLines) == 0 {
		p("\techo \"%s publishes nothing for opkg; this router needs OpenWrt %s or later\" >&2",
			f.Name, firstOr(apkLines, "25.12"))
		p("\texit 1")
	} else {
		p("\tcase \" %s \" in *\" $line \"*) ;; *) unsupported \"%s\" opkg ;; esac",
			strings.Join(ipkLines, " "), strings.Join(ipkLines, ", "))
		p("")
		p("\tarch=${DISTRIB_ARCH:-}")
		p("\t[ -n \"$arch\" ] || { echo \"cannot read DISTRIB_ARCH from /etc/openwrt_release\" >&2; exit 1; }")
		p("")
		p("\t# opkg looks a key up by filename, which must be its id.")
		p("\tmkdir -p /etc/opkg/keys")
		p("\twget -qO /etc/opkg/keys/%s %s/%s", in.UsignKeyID, base, in.UsignKeyID)
		p("")
		p("\t# Replace any previous line for this feed rather than appending a second.")
		p("\ttouch /etc/opkg/customfeeds.conf")
		p("\tsed -i \"/^src\\\\/gz %s /d\" /etc/opkg/customfeeds.conf", f.Name)
		p("\techo \"src/gz %s %s\" >> /etc/opkg/customfeeds.conf", f.Name, ipkRepo)
		p("")
		p("\tmkdir -p /lib/upgrade/keep.d")
		p("\tprintf '%%s\\n' /etc/opkg/keys/%s /etc/opkg/customfeeds.conf > /lib/upgrade/keep.d/%s",
			in.UsignKeyID, f.Name)
		p("")
		p("\topkg update")
		p("\techo \"%s added. Install by name: opkg install <package>\"", title)
	}
	p("fi")
	return b.String()
}

func firstOr(s []string, fallback string) string {
	if len(s) > 0 {
		return s[0]
	}
	return fallback
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
	fmt.Fprintf(&b, "One line, either release line:\n\n")
	fmt.Fprintf(&b, "```sh\nwget -qO- %s/%s | sh\n```\n\n",
		strings.TrimSuffix(f.URL, "/"), ScriptName)
	fmt.Fprintf(&b, "Read it first if you would rather: it is fetched, then run as root.\n\n")
	fmt.Fprintf(&b, "Or by hand, on OpenWrt %s and later:\n\n", release)
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
