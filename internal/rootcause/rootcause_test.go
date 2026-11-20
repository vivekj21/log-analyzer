package rootcause

import (
	"testing"

	"k8s-log-analyzer/internal/llm"
)

func TestCategory(t *testing.T) {
	cases := map[string]string{
		"x\nROOT_CAUSE_CATEGORY: IMAGE_PULL_FAIL\n": "IMAGE_PULL_FAIL",
		"**ROOT_CAUSE_CATEGORY:** oom_killed":       "OOM_KILLED",
		"the pod was CRASHLOOP_BAD_CONFIG as seen":  "CRASHLOOP_BAD_CONFIG",
		"nothing recognisable here":                 "",
		"ROOT_CAUSE_CATEGORY: CANNOT_DETERMINE":     "CANNOT_DETERMINE",
	}
	for in, want := range cases {
		if got := Category(in); got != want {
			t.Errorf("Category(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSummary(t *testing.T) {
	if got := Summary("Confidence: HIGH\n**Root Cause:** Image cannot be pulled\nROOT_CAUSE_CATEGORY: X"); got != "Image cannot be pulled" {
		t.Errorf("got %q", got)
	}
	if got := Summary("\n## The pod ran out of memory\nmore"); got != "The pod ran out of memory" {
		t.Errorf("fallback got %q", got)
	}
}

func TestSeverity(t *testing.T) {
	for cat, want := range map[string]string{"OOM_KILLED": "P1", "IMAGE_PULL_FAIL": "P2", "NO_FAULT": "P3", "CANNOT_DETERMINE": "P2", "": "P2"} {
		if got := Severity(cat); got != want {
			t.Errorf("Severity(%q)=%q want %q", cat, got, want)
		}
	}
}

func TestEveryCategoryIsRecognised(t *testing.T) {
	for _, c := range llm.AllCategories {
		if got := Category("ROOT_CAUSE_CATEGORY: " + c); got != c {
			t.Errorf("Category for %s = %q", c, got)
		}
	}
}
