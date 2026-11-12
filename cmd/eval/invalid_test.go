package main

import "testing"

func TestInvalidRunDetection(t *testing.T) {
	cases := []struct {
		name, report, err string
		want              bool
	}{
		{"gemini error", "Investigation Failed: [GEMINI_ERROR] 429", "", true},
		{"timeout", "", "case investigation timed out", true},
		{"empty report", "   ", "", true},
		{"real answer", "ROOT_CAUSE_CATEGORY: OOM_KILLED\nbecause...", "", false},
	}
	for _, c := range cases {
		if got := isInvalidRun(c.report, c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
