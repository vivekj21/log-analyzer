package llm

import (
	"strings"
	"testing"
)

// The model may only answer with a category the rest of the program understands, and it must be told
// that "nothing wrong" and "cannot tell" are allowed answers (a forced choice makes models invent a fault).
func TestSystemPromptOffersEveryCategoryAndHonestAnswers(t *testing.T) {
	all := []string{CategoryOOMKilled, CategoryCrashLoopBadConfig, CategoryImagePullFail, CategoryReadinessProbeFail,
		CategoryDBLock, CategoryNoFault, CategoryCannotDetermine}
	line := ""
	for _, l := range strings.Split(systemPrompt, "\n") {
		if strings.HasPrefix(l, "ROOT_CAUSE_CATEGORY: [One of:") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("the response format line listing the categories is missing")
	}
	for _, c := range all {
		if !strings.Contains(line, c) {
			t.Errorf("category %s is not offered to the model in: %s", c, line)
		}
	}
	for _, phrase := range []string{"## Honest Answers", "answer NO_FAULT", "answer CANNOT_DETERMINE", "Never name a cause the data does not show"} {
		if !strings.Contains(systemPrompt, phrase) {
			t.Errorf("system prompt lacks %q", phrase)
		}
	}
}
