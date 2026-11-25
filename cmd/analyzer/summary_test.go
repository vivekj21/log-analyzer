package main

import (
	"strings"
	"testing"
)

func TestSummaryOnlySuggestsWhatWasNotUsed(t *testing.T) {
	cases := []struct {
		name   string
		s      runSummary
		has    []string
		hasNot []string
	}{
		{"nothing asked", runSummary{OutputAsked: false},
			[]string{"shown above", "not saved. To keep a copy, add: --output report.md", "not committed. To commit it, add: --git-repo", "No log text was sent anywhere"},
			[]string{"NOT saved", "NOT committed", "was sent to"}},
		{"file saved", runSummary{OutputAsked: true, OutputPath: "/x/r.md"},
			[]string{"saved to /x/r.md"}, []string{"To keep a copy", "NOT saved"}},
		{"file failed", runSummary{OutputAsked: true, OutputFailed: true},
			[]string{"NOT saved (see the warning above)"}, []string{"To keep a copy", "saved to"}},
		{"git committed", runSummary{OutputAsked: true, Git: gitCommitted, GitDetail: "f.md (commit abc1234)"},
			[]string{"committed f.md (commit abc1234)"}, []string{"To commit it", "NOT committed"}},
		{"git failed", runSummary{OutputAsked: true, Git: gitFailed},
			[]string{"NOT committed (see the warning above)"}, []string{"To commit it"}},
		{"sent to model", runSummary{OutputAsked: true, SentToModel: true, Provider: "gemini", Model: "m1"},
			[]string{"masked, then the log text was sent to gemini (m1)", "--llm heuristic"}, []string{"No log text was sent anywhere"}},
		{"model failed", runSummary{OutputAsked: true, SentToModel: true, ModelFailed: true, Provider: "openai", Model: "m2"},
			[]string{"tried to send the log text to openai (m2)", "that service failed"}, []string{"No log text was sent anywhere"}},
	}
	for _, c := range cases {
		got := c.s.String()
		for _, w := range c.has {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in:\n%s", c.name, w, got)
			}
		}
		for _, w := range c.hasNot {
			if strings.Contains(got, w) {
				t.Errorf("%s: should not contain %q in:\n%s", c.name, w, got)
			}
		}
	}
}

func TestShortHash(t *testing.T) {
	if shortHash("0123456789abcdef") != "0123456" || shortHash("abc") != "abc" {
		t.Fatal("shortHash wrong")
	}
}
