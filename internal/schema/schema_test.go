package schema

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite schema/v1.json from the config package")

// Path is the checked-in schema, relative to this package.
const Path = "../../schema/v1.json"

// TestSchemaMatchesConfig is the drift check. The schema is published, so a field
// added to config.go without regenerating it means the file people's editors read
// describes a version of owfeed that no longer exists.
//
// Regenerate with: go test ./internal/schema -update
func TestSchemaMatchesConfig(t *testing.T) {
	got, err := Generate("../config")
	if err != nil {
		t.Fatal(err)
	}

	if *update {
		if err := os.MkdirAll(filepath.Dir(Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(Path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", Path)
		return
	}

	want, err := os.ReadFile(Path)
	if err != nil {
		t.Fatalf("%v\n\nrun: go test ./internal/schema -update", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is out of date with internal/config\n\nrun: go test ./internal/schema -update", Path)
	}
}

func TestSchemaIsValidJSON(t *testing.T) {
	got, err := Generate("../config")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}

	if doc["$id"] != ID {
		t.Errorf("$id is %v", doc["$id"])
	}
	// Unknown keys are an error in owfeed. It is the one rule from validate.go this
	// format can express, and a schema that permitted them would contradict the tool
	// on the single mistake it exists to catch.
	if doc["additionalProperties"] != false {
		t.Error("root allows additional properties")
	}

	props, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatal("no properties")
	}
	for _, key := range []string{"version", "feed", "layout", "releases", "signing", "build", "packages", "publish"} {
		if _, ok := props[key]; !ok {
			t.Errorf("no %q property", key)
		}
	}

	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("no $defs")
	}
	for _, name := range []string{"Feed", "Release", "Signing", "Package", "Publish", "I18n"} {
		if _, ok := defs[name]; !ok {
			t.Errorf("no %q definition", name)
		}
	}
}

// TestDescriptionsSurvive guards the reason this generator reads the AST at all.
// Reflection would produce the same field names with none of the prose, and the
// prose is what stops someone writing a config that parses and then fails.
func TestDescriptionsSurvive(t *testing.T) {
	got, err := Generate("../config")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	defs := doc["$defs"].(map[string]any)

	feed := defs["Feed"].(map[string]any)["properties"].(map[string]any)
	url := feed["url"].(map[string]any)
	desc, _ := url["description"].(string)
	if desc == "" {
		t.Fatal("feed.url has no description")
	}
	// The redirect rule is the single most expensive thing to learn by experiment,
	// because the feed works in a browser the whole time it is broken on a router.
	if !contains(desc, "redirect") {
		t.Errorf("feed.url description does not mention redirects: %q", desc)
	}
}

// TestEnumsComeFromConstants proves the enum values are read out of the code
// rather than transcribed, which is the half of an enum that goes stale.
func TestEnumsComeFromConstants(t *testing.T) {
	got, err := Generate("../config")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	defs := doc["$defs"].(map[string]any)

	release := defs["Release"].(map[string]any)["properties"].(map[string]any)
	format := release["format"].(map[string]any)
	enum, ok := format["enum"].([]any)
	if !ok {
		t.Fatal("releases[].format has no enum")
	}
	// Both lines, because owfeed serves 24.10 as well as 25.12.
	want := map[string]bool{"apk": true, "ipk": true}
	if len(enum) != len(want) {
		t.Fatalf("format enum is %v", enum)
	}
	for _, v := range enum {
		if !want[v.(string)] {
			t.Errorf("unexpected format %v", v)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
