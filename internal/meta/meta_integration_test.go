package meta_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/VizzleTF/owfeed/internal/apk"
	"github.com/VizzleTF/owfeed/internal/testapk"
)

// The version grammar in this package is a port of apk-tools' tokeniser, and a port
// is a claim that can quietly stop being true. `apk version --check` reports every
// argument it considers invalid and exits with the count, so the whole corpus is
// settled in one invocation — against the same apk build that will read the feed.
//
//	OWFEED_INTEGRATION=1 go test ./internal/meta/ -run Integration -v
func TestIntegrationVersionCorpusMatchesAPK(t *testing.T) {
	tool := testapk.Require(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	args := []string{"version", "--check"}
	want := map[string]bool{} // version -> apk should reject it
	for _, tc := range versionCorpus {
		// Entries carrying whitespace would come back indistinguishable from apk's
		// own line framing; nothing in the corpus needs them.
		if strings.ContainsAny(tc.v, " \t\n") || tc.v == "" {
			t.Fatalf("corpus entry %q cannot be cross-checked; keep whitespace out of it", tc.v)
		}
		args = append(args, tc.v)
		want[tc.v] = !tc.valid
	}

	res, err := tool.Run(ctx, apk.Invocation{Args: args})
	if err != nil {
		t.Fatalf("apk version --check: %v", err)
	}

	rejected := map[string]bool{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			rejected[line] = true
		}
	}

	for v, wantRejected := range want {
		switch {
		case wantRejected && !rejected[v]:
			t.Errorf("apk accepts %q but owfeed rejects it", v)
		case !wantRejected && rejected[v]:
			t.Errorf("apk rejects %q but owfeed accepts it", v)
		}
	}
	if got := len(rejected); res.ExitCode != got {
		t.Errorf("apk exited %d but named %d invalid versions; output was:\n%s", res.ExitCode, got, res.Stdout)
	}
}
