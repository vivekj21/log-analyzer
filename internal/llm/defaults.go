package llm

import (
	"os"
	"strings"
)

// DefaultModel is the one place the built-in model name lives.
// Override with the GEMINI_MODEL variable or the --model flag.
const DefaultModel = "gemini-3.5-flash-lite"

func ModelFromEnv() string {
	if m := strings.TrimSpace(os.Getenv("GEMINI_MODEL")); m != "" {
		return m
	}
	return DefaultModel
}
