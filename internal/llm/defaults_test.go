package llm

import "testing"

func TestModelFromEnv(t *testing.T) {
	t.Setenv("GEMINI_MODEL", "")
	if ModelFromEnv() != DefaultModel {
		t.Fatal("empty env must give the default")
	}
	t.Setenv("GEMINI_MODEL", "  x-model ")
	if got := ModelFromEnv(); got != "x-model" {
		t.Fatalf("got %q", got)
	}
}
