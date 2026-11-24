package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s-log-analyzer/internal/protocol"
)

// scripted returns its answers in order and records the prompts it was sent.
type scripted struct {
	answers []*AnalysisResult
	errs    []error
	prompts []string
}

func (s *scripted) next(p string) (*AnalysisResult, error) {
	i := len(s.prompts)
	s.prompts = append(s.prompts, p)
	var e error
	if i < len(s.errs) {
		e = s.errs[i]
	}
	return s.answers[i], e
}
func (s *scripted) Analyze(_ context.Context, _, p string) (*AnalysisResult, error) { return s.next(p) }
func (s *scripted) SendToolResults(_ context.Context, _ string, _ []protocol.ToolCallResult) (*AnalysisResult, error) {
	return s.next("tool results")
}

func text(t string) *AnalysisResult { return &AnalysisResult{TextResponse: t, TotalTokens: 10} }

func TestCategoryGuardKeepsAValidAnswer(t *testing.T) {
	in := text("Root Cause: x\nROOT_CAUSE_CATEGORY: OOM_KILLED\n")
	s := &scripted{answers: []*AnalysisResult{in}}
	got, err := NewCategoryGuard(s).SendToolResults(context.Background(), "i", nil)
	if err != nil || got.TextResponse != in.TextResponse || len(s.prompts) != 1 {
		t.Fatalf("a valid answer must pass untouched with no extra call: %v %q calls=%d", err, got.TextResponse, len(s.prompts))
	}
}

func TestCategoryGuardLeavesToolCallsAlone(t *testing.T) {
	s := &scripted{answers: []*AnalysisResult{{HasToolCalls: true}}}
	got, _ := NewCategoryGuard(s).Analyze(context.Background(), "i", "p")
	if !got.HasToolCalls || len(s.prompts) != 1 {
		t.Fatalf("tool calls must pass untouched, calls=%d", len(s.prompts))
	}
}

func TestCategoryGuardAsksOnceAndAppendsTheLine(t *testing.T) {
	// the real failure: prose says OOMKilled, no category line
	s := &scripted{answers: []*AnalysisResult{
		text("**Root Cause:** limit 50Mi, stress needs 200M, exit code 137 (OOMKilled)."),
		text("ROOT_CAUSE_CATEGORY: OOM_KILLED"),
	}}
	got, err := NewCategoryGuard(s).SendToolResults(context.Background(), "i", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.prompts) != 2 || !strings.Contains(s.prompts[1], "ROOT_CAUSE_CATEGORY") {
		t.Fatalf("expected one repair request, got %v", s.prompts)
	}
	if c, ok := ValidCategoryLine(got.TextResponse); !ok || c != CategoryOOMKilled {
		t.Fatalf("category line missing or wrong: %q", got.TextResponse)
	}
	if !strings.Contains(got.TextResponse, "exit code 137") || got.TotalTokens != 20 {
		t.Fatalf("original text and token count must be kept: %q %d", got.TextResponse, got.TotalTokens)
	}
}

func TestCategoryGuardFallsBackToCannotDetermine(t *testing.T) {
	cases := map[string]*scripted{
		"repair has no category": {answers: []*AnalysisResult{text("no line"), text("I think it was memory")}},
		"repair names unknown":   {answers: []*AnalysisResult{text("no line"), text("ROOT_CAUSE_CATEGORY: MEMORY_THING")}},
		"repair fails":           {answers: []*AnalysisResult{text("no line"), nil}, errs: []error{nil, errors.New("rate limit")}},
		"repair calls a tool":    {answers: []*AnalysisResult{text("no line"), {HasToolCalls: true}}},
	}
	for name, s := range cases {
		got, err := NewCategoryGuard(s).SendToolResults(context.Background(), "i", nil)
		if err != nil {
			t.Fatalf("%s: a failed repair must not fail the investigation: %v", name, err)
		}
		c, ok := ValidCategoryLine(got.TextResponse)
		if !ok || c != CategoryCannotDetermine || !strings.Contains(got.TextResponse, "did not name a valid") {
			t.Fatalf("%s: want CANNOT_DETERMINE with a note, got %q", name, got.TextResponse)
		}
		if len(s.prompts) != 2 {
			t.Fatalf("%s: must ask exactly once, asked %d times", name, len(s.prompts)-1)
		}
	}
}

func TestCategoryGuardPassesErrorsThrough(t *testing.T) {
	s := &scripted{answers: []*AnalysisResult{nil}, errs: []error{errors.New("boom")}}
	if _, err := NewCategoryGuard(s).Analyze(context.Background(), "i", "p"); err == nil || len(s.prompts) != 1 {
		t.Fatalf("a model error must be returned as is")
	}
}

func TestValidCategoryLine(t *testing.T) {
	for in, want := range map[string]string{
		"ROOT_CAUSE_CATEGORY: DB_LOCK":      "DB_LOCK",
		"**ROOT_CAUSE_CATEGORY:** NO_FAULT": "NO_FAULT",
		"root_cause_category: dns_fail":     "DNS_FAIL",
		"ROOT_CAUSE_CATEGORY: [One of: X]":  "",
		"it was OOM_KILLED":                 "",
		"ROOT_CAUSE_CATEGORY: OOM":          "",
	} {
		if got, _ := ValidCategoryLine(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
