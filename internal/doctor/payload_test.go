package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"owfeed.org/owfeed/internal/config"
)

func payloadInput(t *testing.T) (Input, string) {
	t.Helper()
	in := input(t, config.Package{Name: "luci-app-demo", Files: "root"})
	root := filepath.Join(in.Root, "root")
	return in, root
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// rpcd skips an acl.d file it cannot parse and says nothing, so the package's ACL is
// granted to nobody and every RPC the page makes answers "Access denied".
func TestPayloadJSON(t *testing.T) {
	in, root := payloadInput(t)
	put(t, filepath.Join(root, "usr/share/rpcd/acl.d/luci-app-demo.json"),
		`{"luci-app-demo": {"description": "demo",}}`)
	put(t, filepath.Join(root, "usr/share/luci/menu.d/luci-app-demo.json"),
		`{"admin/demo": {"title": "Demo"}}`)

	r := &Report{}
	checkPayloadJSON(r, in)
	if len(r.Findings) != 1 || r.Findings[0].ID != "OWF212" {
		t.Fatalf("findings = %v, want one OWF212", ids(r))
	}
	if !strings.Contains(r.Findings[0].Where, "acl.d") {
		t.Errorf("finding does not name the broken file:\n%s", r.Findings[0])
	}

	// The valid one on its own is not a finding.
	if err := os.Remove(filepath.Join(root, "usr/share/rpcd/acl.d/luci-app-demo.json")); err != nil {
		t.Fatal(err)
	}
	r = &Report{}
	checkPayloadJSON(r, in)
	if len(r.Findings) != 0 {
		t.Errorf("valid JSON was reported: %v", ids(r))
	}
}

// /etc/uci-defaults runs once at first boot and OpenWrt deletes it whether it worked or
// not, so a syntax error there is a package that installed cleanly and did nothing.
func TestPayloadShell(t *testing.T) {
	in, root := payloadInput(t)
	// A missing `fi`, which is a parse error rather than a runtime one: `[ -n "$x" ;`
	// would still PARSE — `[` is an ordinary command and the missing bracket only
	// fails when it runs.
	put(t, filepath.Join(root, "etc/uci-defaults/30_luci-app-demo"),
		"#!/bin/sh\nif [ -n \"$x\" ]; then\n\t:\n")

	r := &Report{}
	checkPayloadShell(r, in)
	if len(r.Findings) != 1 || r.Findings[0].ID != "OWF213" {
		t.Fatalf("findings = %v, want one OWF213", ids(r))
	}
	if !strings.Contains(r.Findings[0].Where, "30_luci-app-demo") {
		t.Errorf("finding does not name the file:\n%s", r.Findings[0])
	}

	// Fixed, and an init script with OpenWrt's own two-argument shebang alongside it.
	put(t, filepath.Join(root, "etc/uci-defaults/30_luci-app-demo"),
		"#!/bin/sh\nif [ -n \"$x\" ]; then :; fi\n")
	put(t, filepath.Join(root, "etc/init.d/demo"),
		"#!/bin/sh /etc/rc.common\nSTART=99\nstart_service() { :; }\n")

	r = &Report{}
	checkPayloadShell(r, in)
	if len(r.Findings) != 0 {
		t.Errorf("scripts that parse were reported: %v\n%s", ids(r), r.Findings[0])
	}
}

// A payload with no shell and no JSON is the common case — a theme is CSS and
// templates — and it must not be a finding.
func TestPayloadIgnoresEverythingElse(t *testing.T) {
	in, root := payloadInput(t)
	put(t, filepath.Join(root, "www/luci-static/demo/cascade.css"), "a{color:red}\n")
	put(t, filepath.Join(root, "usr/share/ucode/luci/template/demo/header.ut"), "{{ x }}\n")

	r := &Report{}
	checkPayloadJSON(r, in)
	checkPayloadShell(r, in)
	if len(r.Findings) != 0 {
		t.Errorf("a payload with nothing to parse was reported: %v", ids(r))
	}
	if r.Checked == 0 {
		t.Error("the checks did not run at all")
	}
}

// A script asking for bash is not one `sh -n` can judge, and OpenWrt has no bash.
func TestPayloadSkipsBash(t *testing.T) {
	in, root := payloadInput(t)
	put(t, filepath.Join(root, "usr/libexec/demo-helper"),
		"#!/bin/bash\n[[ -n $x ]] && echo yes\n")

	r := &Report{}
	checkPayloadShell(r, in)
	if len(r.Findings) != 0 {
		t.Errorf("a bash script was judged by sh: %v\n%s", ids(r), r.Findings[0])
	}
}
