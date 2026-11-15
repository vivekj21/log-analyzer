package llm

import "testing"

func TestEvaluateRulesRequiredSettingMissing(t *testing.T) {
	cat, _, ok := evaluateRules("FATAL: required setting DATABASE_URL is missing, invalid configuration")
	if !ok || cat != CategoryCrashLoopBadConfig {
		t.Fatalf("got %q ok=%v", cat, ok)
	}
}
