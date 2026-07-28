// Package meta holds the metadata rules apk enforces on a package, expressed as
// checks that run before apk does.
//
// apk validates all of this itself and rejects what it does not like, but its
// diagnostics are terse ("invalid version") and arrive after a build has already
// run. Restating the rules here buys two things: the failure lands on the config
// line that caused it, and `owfeed doctor` can report it without building anything.
//
// Everything here is a port of apk-tools v3.0.5 source, cited per rule. Where the
// port and apk disagree, apk is right — the integration test cross-checks a corpus
// of versions against the real binary for exactly that reason.
package meta

import (
	"fmt"
	"strings"
)

// Version tokens, in apk's order. The order is load-bearing: the parser decides
// whether a token may follow the previous one by comparing these values, so
// reordering them silently changes the accepted grammar.
//
// From src/version.c, enum PARTS.
const (
	tokInitialDigit = iota
	tokDigit
	tokLetter
	tokSuffix
	tokSuffixNo
	tokCommitHash
	tokRevisionNo
	tokEnd
	tokInvalid
)

// versionSuffixes is apk's fixed set, from DECLARE_SUFFIXES in src/version.c.
// A suffix outside this set is not a version, however plausible it looks:
// "_dev", "_final" and "_release" are all rejected.
var versionSuffixes = []string{"alpha", "beta", "pre", "rc", "cvs", "svn", "git", "hg", "p"}

// VersionError describes why a version string is not one.
type VersionError struct {
	Version string
	// Pos is the byte offset where parsing stopped.
	Pos  int
	Msg  string
	Hint string
}

func (e *VersionError) Error() string {
	s := fmt.Sprintf("version %q: %s", e.Version, e.Msg)
	if e.Hint != "" {
		s += "\n  " + e.Hint
	}
	return s
}

// ValidateVersion reports whether v is a version apk will accept.
//
// The grammar, from the comment at the top of src/version.c:
//
//	digit{.digit}...{letter}{_suf{#}}...{~hash}{-r#}
//
// A version that fails here is not a near miss that apk will tolerate. apk sorts
// packages by these tokens, so a string it cannot tokenise has no position in the
// ordering at all and is refused outright.
func ValidateVersion(v string) error {
	if v == "" {
		return &VersionError{Version: v, Msg: "is empty"}
	}

	b := v
	pos := func() int { return len(v) - len(b) }

	// token_first: a version always opens with a run of decimal digits.
	n := digitRun(b)
	if n == 0 {
		return &VersionError{
			Version: v, Pos: 0,
			Msg:  "does not start with a digit",
			Hint: "apk versions begin with a number; a leading \"v\" is part of a tag name, not of a version",
		}
	}
	b = b[n:]
	tok := tokInitialDigit

	for len(b) > 0 {
		c := b[0]
		switch {
		case c >= 'a' && c <= 'z':
			if tok == tokCommitHash {
				// The whole word after ~ was meant as a tag, and apk ate the leading
				// hex-looking part of it as a commit hash: in "1.0~beta" it consumed
				// "be" and stopped at "t". Reporting the offset of the "t" describes
				// the parse rather than the mistake.
				return &VersionError{
					Version: v, Pos: pos(),
					Msg:  fmt.Sprintf("has a word after ~ at offset %d, but apk reads everything after ~ as a commit hash", tildeAt(v)+1),
					Hint: "only lowercase hex may follow ~; for a pre-release use _pre1, _beta1 or _rc1, which is what apk sorts before the release",
				}
			}
			if tok > tokDigit {
				return &VersionError{
					Version: v, Pos: pos(),
					Msg:  fmt.Sprintf("has a letter at offset %d, where one is not allowed", pos()),
					Hint: "a single trailing letter may follow the numbers (1.2.3a), but nothing may follow a suffix, a commit hash, or a revision",
				}
			}
			b = b[1:]
			tok = tokLetter

		case c == '.', c >= '0' && c <= '9':
			if c == '.' {
				if tok > tokDigit {
					return &VersionError{
						Version: v, Pos: pos(),
						Msg: fmt.Sprintf("has a dot at offset %d, after a part that ends the numeric section", pos()),
					}
				}
				b = b[1:]
			}
			switch tok {
			case tokInitialDigit, tokDigit:
				tok = tokDigit
			case tokSuffix:
				// The number that qualifies a suffix, as in _pre2.
				tok = tokSuffixNo
			default:
				return &VersionError{
					Version: v, Pos: pos(),
					Msg: fmt.Sprintf("has a digit at offset %d, where one is not allowed", pos()),
				}
			}
			n := digitRun(b)
			if n == 0 {
				return &VersionError{
					Version: v, Pos: pos(),
					Msg: fmt.Sprintf("expects a number at offset %d", pos()),
				}
			}
			b = b[n:]

		case c == '_':
			if tok > tokSuffixNo {
				return &VersionError{
					Version: v, Pos: pos(),
					Msg: fmt.Sprintf("has a suffix at offset %d, after the commit hash or revision", pos()),
				}
			}
			b = b[1:]
			word := lowerRun(b)
			if !isVersionSuffix(word) {
				return &VersionError{
					Version: v, Pos: pos(),
					Msg:  fmt.Sprintf("has unknown suffix %q", "_"+word),
					Hint: "apk knows only _" + strings.Join(versionSuffixes, ", _"),
				}
			}
			b = b[len(word):]
			tok = tokSuffix

		case c == '~':
			if tok >= tokCommitHash {
				return &VersionError{
					Version: v, Pos: pos(),
					Msg: fmt.Sprintf("has a second commit hash at offset %d", pos()),
				}
			}
			b = b[1:]
			n := hexRun(b)
			if n == 0 {
				return &VersionError{
					Version: v, Pos: pos(),
					Msg: "has ~ with no hex digits after it",
					// The mistake this catches is writing 1.0~beta for a pre-release.
					// apk reads everything after ~ as a commit hash, and "beta" is not
					// one — b, e and a are hex, but t is not.
					Hint: "after ~ apk accepts only lowercase hex, because it reads that part as a commit hash; for a pre-release use _pre1, _beta1 or _rc1 instead",
				}
			}
			b = b[n:]
			tok = tokCommitHash

		case c == '-':
			if tok >= tokRevisionNo {
				return &VersionError{
					Version: v, Pos: pos(),
					Msg: fmt.Sprintf("has a second revision at offset %d", pos()),
				}
			}
			if !strings.HasPrefix(b, "-r") {
				return &VersionError{
					Version: v, Pos: pos(),
					Msg:  fmt.Sprintf("has a dash at offset %d that does not begin a revision", pos()),
					Hint: "the only thing a dash may introduce is -r followed by a number; a dash is otherwise the separator between a package name and its version, so apk would not be able to tell the two apart",
				}
			}
			b = b[2:]
			n := digitRun(b)
			if n == 0 {
				return &VersionError{
					Version: v, Pos: pos(),
					Msg: "has -r with no number after it",
				}
			}
			b = b[n:]
			tok = tokRevisionNo

		default:
			return &VersionError{
				Version: v, Pos: pos(),
				Msg: fmt.Sprintf("has %q at offset %d, which is not part of any version", string(c), pos()),
			}
		}
	}
	return nil
}

// VersionAdvice returns notes about a valid version that are worth saying out loud.
// These are not errors: apk accepts every string this is called with.
func VersionAdvice(v string) []string {
	var out []string
	if strings.Contains(v, "~") {
		out = append(out, "the ~ in this version is a commit hash marker; openwrt#17237 reports downloads hanging on package names containing it, so prefer _git<n> or _p<n> if you have the choice")
	}
	if i := strings.Index(v, "-r"); i < 0 {
		out = append(out, "this version has no -r revision; without one you cannot ship a rebuild of the same upstream version, because apk would consider the two equal and never upgrade")
	}
	return out
}

func tildeAt(v string) int { return strings.IndexByte(v, '~') }

func isVersionSuffix(s string) bool {
	for _, suf := range versionSuffixes {
		if s == suf {
			return true
		}
	}
	return false
}

func digitRun(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

// lowerRun spans APK_CTYPE_VERSION_SUFFIX, which is exactly [a-z] (src/ctype.c).
func lowerRun(s string) string {
	i := 0
	for i < len(s) && s[i] >= 'a' && s[i] <= 'z' {
		i++
	}
	return s[:i]
}

// hexRun spans APK_CTYPE_HEXDIGIT, which is [0-9a-f] — lowercase only, so an
// uppercase git hash is not a commit hash as far as apk is concerned.
func hexRun(s string) int {
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			i++
			continue
		}
		break
	}
	return i
}
