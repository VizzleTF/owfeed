package meta

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxDescriptionBytes is where LuCI's package view starts truncating (luci#8561).
// apk itself imposes no limit, so this is advice rather than a rule — but a
// description nobody can read in the one UI most users have is not a description.
const MaxDescriptionBytes = 512

// infoFields is the set of keys `apk mkpkg --info` accepts, from schema_pkginfo in
// src/apk_adb.c. A key outside it makes mkpkg exit with "invalid info field".
//
// The value is what owfeed does with the field, which is not always what apk does:
// see computedFields.
var infoFields = map[string]bool{
	"name":              true,
	"version":           true,
	"hashes":            true,
	"description":       true,
	"arch":              true,
	"license":           true,
	"origin":            true,
	"maintainer":        true,
	"url":               true,
	"repo-commit":       true,
	"build-time":        true,
	"installed-size":    true,
	"file-size":         true,
	"provider-priority": true,
	"depends":           true,
	"provides":          true,
	"replaces":          true,
	"install-if":        true,
	"recommends":        true,
	"layer":             true,
	"tags":              true,
	// From schema_package rather than schema_pkginfo; parse_info accepts it as a
	// special case.
	"replaces-priority": true,
}

// computedFields are fields whose value comes from the package contents. apk
// rejects installed-size and file-size outright (parse_info jumps to the error
// path for both); hashes it accepts on the command line but then overwrites while
// packing, so a value passed there is not wrong so much as inert — which is worse,
// because it looks like it took effect.
var computedFields = map[string]string{
	"installed-size": "apk sums the payload itself and refuses this field on the command line",
	"file-size":      "apk measures the finished package itself and refuses this field on the command line",
	"hashes":         "apk computes the payload hashes while packing and overwrites anything passed here",
}

// ValidateInfo checks one --info key and value.
//
// owfeed executes apk directly rather than through a shell, so the quoting dance
// in OpenWrt's package-pack.mk (description_escape backslash-escapes \ " $ and `)
// does not apply here: those characters reach apk intact and mean nothing to it.
// The shell only re-enters the picture in `--print-commands` output, and quoting
// belongs there, at the point where a shell will actually read it.
//
// What must be rejected is what breaks regardless of shell: a value carrying a
// newline or a NUL, which apk stores verbatim and which then splits or truncates
// the field wherever the index is later read as text.
func ValidateInfo(key, value string) error {
	if key == "" {
		return fmt.Errorf("info field has no name")
	}
	if reason, computed := computedFields[key]; computed {
		return fmt.Errorf("info field %q is computed from the package: %s", key, reason)
	}
	if !infoFields[key] {
		return fmt.Errorf("%q is not an apk package field\n  known fields: %s", key, strings.Join(knownInfoFields(), ", "))
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("info field %q is not valid UTF-8", key)
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 || c == 0x7f {
			return fmt.Errorf("info field %q contains a control character (%#02x) at offset %d\n  "+
				"apk stores the value verbatim, so a newline here splits the field for every tool that reads the index as text", key, c, i)
		}
	}
	return nil
}

func knownInfoFields() []string {
	out := make([]string, 0, len(infoFields))
	for k := range infoFields {
		if _, computed := computedFields[k]; computed {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ScriptTypes is apk's set of package scripts, from schema_scripts in src/apk_adb.c.
var ScriptTypes = []string{
	"trigger",
	"pre-install", "post-install",
	"pre-deinstall", "post-deinstall",
	"pre-upgrade", "post-upgrade",
}

// ValidateArch rejects the one arch value that looks right and is not.
func ValidateArch(arch string) error {
	switch arch {
	case "":
		return fmt.Errorf("arch is empty")
	case "all":
		return fmt.Errorf("arch \"all\" is not installable under apk\n  " +
			"use \"noarch\"; this is the translation OpenWrt's own package-pack.mk applies to LUCI_PKGARCH:=all")
	}
	return nil
}

// ABISuffix returns the suffix appended to a package name for ABI version abi,
// following OpenWrt's FormatABISuffix (include/feeds.mk): a dash appears only when
// the base name already ends in a digit, which is why the ecosystem carries both
// libjson-c5 and libblobmsg-json20260213.
func ABISuffix(name, abi string) string {
	if abi == "" {
		return ""
	}
	if n := len(name); n > 0 && name[n-1] >= '0' && name[n-1] <= '9' {
		return "-" + abi
	}
	return abi
}
