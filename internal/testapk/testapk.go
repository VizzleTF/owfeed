// Package testapk hands tests a real apk-tools 3.x, or skips them.
//
// Several packages need to check their idea of apk's behaviour against apk itself
// rather than against a restatement of it, which is the only way those claims stay
// true across an apk-tools release. Acquiring the toolchain is expensive enough that
// it is opt-in and resolved once per process.
package testapk

import (
	"context"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"owfeed.org/owfeed/internal/apk"
)

// Release is the point release whose host apk these tests run against.
const Release = "25.12.5"

var (
	once   sync.Once
	shared *apk.Tool
	sdkDir string
	err    error
)

// Require returns a resolved apk, skipping the test if the environment has not
// opted in. Set OWFEED_TEST_CACHE to a stable directory to keep the extracted
// toolchain between runs.
func Require(t *testing.T) *apk.Tool {
	t.Helper()
	if os.Getenv("OWFEED_INTEGRATION") == "" {
		t.Skip("set OWFEED_INTEGRATION=1 to run (downloads an SDK, needs Docker off linux/amd64)")
	}

	once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()

		cache := os.Getenv("OWFEED_TEST_CACHE")
		if cache == "" {
			// Not t.TempDir: the toolchain is shared by every test in the process,
			// so it must outlive whichever one happened to acquire it.
			cache, err = os.MkdirTemp("", "owfeed-test-*")
			if err != nil {
				return
			}
		}
		sdkDir, err = apk.Acquire(ctx, http.DefaultClient, cache, Release)
		if err != nil {
			return
		}
		shared, err = apk.Resolve(ctx, apk.Options{SDKDir: sdkDir, AllowContainer: true})
	})

	if err != nil {
		t.Fatalf("acquiring apk: %v", err)
	}
	return shared
}
