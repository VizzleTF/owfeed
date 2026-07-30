package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const planConfig = `version: 1
feed:
  name: demo
  url: https://github.com/someone/demo
  maintainer: "Someone <someone@example.org>"
releases:
  - line: "25.12"
    default: true
    format: apk
  - line: "24.10"
    format: ipk
publish:
  - target: github-pages
packages:
  - name: luci-app-demo
    build: mkpkg
    arch: noarch
    version: 1.2.0-r1
    files: ./root
    description: "A demo."
    url: https://github.com/someone/demo
    conffiles: ["/etc/config/demo"]
`

func planApp(t *testing.T, body string) (*app, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "owfeed.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	return &app{configPath: filepath.Join(root, "owfeed.yml"), out: out, err: &bytes.Buffer{}}, out
}

// The plan has to name the files `build` will actually write, including the rename
// opkg forces: the architecture apk calls noarch is `all`, and the directory changes
// with it. A plan that disagrees with the build is worse than no plan.
func TestPlanNamesWhatBuildWillWrite(t *testing.T) {
	a, out := planApp(t, planConfig)
	if err := a.plan(t.Context(), []string{"--json"}); err != nil {
		t.Fatal(err)
	}

	var p Plan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Packages) != 1 || len(p.Packages[0].Artifacts) != 2 {
		t.Fatalf("plan = %+v, want one package with two artifacts", p.Packages)
	}

	got := map[string]string{}
	for _, art := range p.Packages[0].Artifacts {
		got[art.Line] = art.Path
	}
	want := map[string]string{
		"25.12": "dist/noarch/luci-app-demo-1.2.0-r1.apk",
		"24.10": "dist/all/luci-app-demo_1.2.0-r1_all.ipk",
	}
	for line, path := range want {
		if got[line] != path {
			t.Errorf("%s artifact = %q, want %q", line, got[line], path)
		}
	}
}

// A package that declares one release line must not be planned onto the other. This
// is the question a maintainer cannot answer by reading the config, and answering it
// wrong means publishing a package the other line's routers cannot resolve.
func TestPlanHonoursReleaseLines(t *testing.T) {
	a, out := planApp(t, strings.Replace(planConfig,
		"    conffiles: [\"/etc/config/demo\"]\n",
		"    conffiles: [\"/etc/config/demo\"]\n    releases: [\"25.12\"]\n", 1))
	if err := a.plan(t.Context(), []string{"--json"}); err != nil {
		t.Fatal(err)
	}

	var p Plan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if n := len(p.Packages[0].Artifacts); n != 1 {
		t.Fatalf("%d artifacts, want 1: %+v", n, p.Packages[0].Artifacts)
	}
	if line := p.Packages[0].Artifacts[0].Line; line != "25.12" {
		t.Errorf("planned onto %s, want 25.12 only", line)
	}
}

// An unresolvable version is the normal state before the build script has run —
// `version-from: file:./dist/VERSION` names a file that does not exist yet. Refusing
// to say anything would make plan useless at the one moment it is most wanted.
func TestPlanSurvivesAnUnresolvedVersion(t *testing.T) {
	a, out := planApp(t, strings.Replace(planConfig,
		"    version: 1.2.0-r1\n", "    version-from: file:./dist/VERSION\n", 1))
	if err := a.plan(t.Context(), nil); err != nil {
		t.Fatalf("plan refused to run: %v", err)
	}

	s := out.String()
	for _, want := range []string{
		"version not resolved yet", // said, not hidden
		"dist/VERSION",             // and it names what is missing
		"version-from: tag",        // and what to do instead
		"<version>",                // a placeholder, not `demo-.apk`
		"/etc/config/demo",         // everything knowable is still printed
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plan output does not mention %q:\n%s", want, s)
		}
	}
}

// A feed that carries only other people's packages builds nothing, and saying so is
// the whole output.
func TestPlanOfAFeedThatBuildsNothing(t *testing.T) {
	a, out := planApp(t, strings.Split(planConfig, "packages:")[0]+"packages: []\n")
	if err := a.plan(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "builds nothing") {
		t.Errorf("output = %q", out.String())
	}
}
