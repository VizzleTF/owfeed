package verify

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The one failure `owfeed publish` cannot see. It refuses a tree that has no
// `.nojekyll`, and it is right to — but what it inspects is the tree, and what
// subscribers fetch is whatever the upload step put in the artifact.
// actions/upload-pages-artifact stopped including dotfiles by default in v4, so a
// feed can be correct everywhere owfeed looks and still be deployed without the file
// that keeps it correct.
func TestNoJekyllIsCheckedAgainstTheLiveSite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		finding bool
	}{
		{"deployed", http.StatusOK, false},
		{"dropped by the upload step", http.StatusNotFound, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/.nojekyll" {
					w.WriteHeader(tc.status)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()

			r := &Report{}
			checkNoJekyll(r, client(), srv.URL)

			if got := len(r.Findings) > 0; got != tc.finding {
				t.Fatalf("finding = %v, want %v (findings: %v)", got, tc.finding, r.Findings)
			}
			if tc.finding && r.Findings[0].ID != "OWF514" {
				t.Errorf("ID = %q, want OWF514", r.Findings[0].ID)
			}
		})
	}
}

// A host that is unreachable is not a feed that is broken. Reporting one as the
// other is how a check earns the habit of being ignored.
func TestNoJekyllStaysQuietWhenTheHostIsUnreachable(t *testing.T) {
	r := &Report{}
	checkNoJekyll(r, client(), "http://127.0.0.1:1")
	if len(r.Findings) != 0 {
		t.Fatalf("findings = %v, want none", r.Findings)
	}
}
