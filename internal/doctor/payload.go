package doctor

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Checks that read the payload a package is about to ship, rather than its config or
// its built index.
//
// Both failures below are SILENT ON THE ROUTER. Nothing logs, nothing exits non-zero,
// and the package installs cleanly — the feature it broke simply is not there. That is
// what makes them worth a check rather than a code review: they are invisible right up
// until a user reports that something does not work, on a router nobody can inspect.

// 212: every JSON file in the payload parses.
//
// LuCI and rpcd both fail CLOSED and QUIETLY on malformed JSON. rpcd skips an acl.d
// file it cannot parse, so the package's ACL grant goes to nobody and every RPC the
// page makes answers "Access denied"; LuCI skips a menu.d file, so the entry simply
// never appears. A trailing comma is enough for either, and no build step notices.
func checkPayloadJSON(r *Report, in Input) {
	forEachPayloadFile(r, in, func(p, root, path, rel string) {
		if filepath.Ext(path) != ".json" {
			return
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if json.Valid(b) {
			return
		}
		r.add(Finding{
			ID: "OWF212", Severity: Error, Where: p + ": " + rel,
			What: "is not valid JSON",
			Why: "rpcd skips an acl.d file it cannot parse and LuCI skips a menu.d file, both without a word — " +
				"the ACL is granted to nobody, or the menu entry never appears",
			Fix: "python3 -m json.tool " + rel + " names the line",
		})
	})
}

// 213: every shell script in the payload parses.
//
// /etc/uci-defaults/* is the case that costs the most. It runs ONCE, at first boot
// after install, and OpenWrt DELETES it whether it succeeded or not — so a syntax
// error there means the package's registration never happens and the evidence removes
// itself. For a theme that is a router that installed correctly and is still using the
// old theme; for an app, a service that is never enabled.
//
// `sh -n` rather than a parser of our own: it is the same parse the router will do,
// and every machine that can run owfeed has one.
func checkPayloadShell(r *Report, in Input) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		return
	}
	forEachPayloadFile(r, in, func(p, root, path, rel string) {
		if !isShell(path, rel) {
			return
		}
		out, err := exec.Command(sh, "-n", path).CombinedOutput()
		if err == nil {
			return
		}
		r.add(Finding{
			ID: "OWF213", Severity: Error, Where: p + ": " + rel,
			What: "does not parse: " + firstLine(string(out)),
			Why: "a broken /etc/uci-defaults script runs once at first boot and is deleted whether it worked or not, " +
				"so the package's registration never happens and nothing is left to say why",
			Fix: "sh -n " + rel,
		})
	})
}

// isShell decides what to parse. Extension first, then the shebang, because the files
// that matter most carry no extension: /etc/uci-defaults/30_luci-theme-example and
// /etc/init.d/example are named the way the router expects and nothing else.
func isShell(path, rel string) bool {
	if strings.HasSuffix(rel, ".sh") {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := f.Read(head)
	line := string(head[:n])
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if !strings.HasPrefix(line, "#!") {
		return false
	}
	// /bin/sh, /bin/ash, and OpenWrt's own `#!/bin/sh /etc/rc.common`. Not bash: a
	// script that asks for bash is not one `sh -n` can judge, and OpenWrt has no bash.
	return strings.Contains(line, "/sh") || strings.Contains(line, "/ash")
}

// forEachPayloadFile walks what each package will ship. The payload is `files:`, the
// same tree checkConffileCoverage reads, so a package that builds from somewhere else
// is skipped rather than guessed at.
func forEachPayloadFile(r *Report, in Input, fn func(pkg, root, path, rel string)) {
	for _, p := range in.Config.Packages {
		if p.Files == "" {
			continue
		}
		r.Checked++
		root := filepath.Join(in.Root, p.Files)

		var files []string
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			files = append(files, path)
			return nil
		})
		// Sorted, so a run over the same tree reports the same findings in the same
		// order: a check whose output moves is one nobody can diff.
		sort.Strings(files)

		for _, path := range files {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				continue
			}
			fn(p.EffectiveName(), root, path, "/"+filepath.ToSlash(rel))
		}
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
