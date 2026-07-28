package main

import (
	"slices"
	"testing"
)

// The apk/opkg boundary is the one fact owfeed and owlab both hold, and the whole
// point of `owfeed releases` is to make a disagreement about it visible. A wrong
// verdict here is a feed that indexes with the wrong tool for a whole release
// line, so the boundary itself is pinned rather than left to a comment.
func TestFormatForIsTheSwitchAt2512(t *testing.T) {
	for _, tc := range []struct {
		line, want string
	}{
		{"25.12", "apk"},
		{"26.03", "apk"},
		{"30.01", "apk"},
		{"snapshot", "apk"},
		{"24.10", "ipk"},
		{"25.11", "ipk"},
		{"23.05", "ipk"},
		{"19.07", "ipk"},
	} {
		if got := FormatFor(tc.line); got != tc.want {
			t.Errorf("FormatFor(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestFormatForRefusesWhatIsNotALine(t *testing.T) {
	for _, line := range []string{"", "25", "25.12.5", "latest", "x.y"} {
		if got := FormatFor(line); got != "" {
			t.Errorf("FormatFor(%q) = %q, want the empty verdict", line, got)
		}
	}
}

// A release candidate is not a line. A feed whose line list picked one up would
// offer subscribers a line that stops existing when the final release lands.
func TestLinesOfSkipsCandidatesAndSortsNewestFirst(t *testing.T) {
	got := linesOf([]string{
		"24.10.8", "25.12.0-rc1", "25.12.5", "23.05.6", "25.12.1", "24.10.2", "snapshot",
	})
	want := []string{"25.12", "24.10", "23.05"}
	if !slices.Equal(got, want) {
		t.Errorf("linesOf = %v, want %v", got, want)
	}
}
