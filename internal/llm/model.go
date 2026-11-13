package llm

import (
	"context"

	"k8s-log-analyzer/internal/protocol"
)

// Root cause categories for classification and evaluation.
const (
	CategoryOOMKilled          = "OOM_KILLED"
	CategoryCrashLoopBadConfig = "CRASHLOOP_BAD_CONFIG"
	CategoryImagePullFail      = "IMAGE_PULL_FAIL"
	CategoryReadinessProbeFail = "READINESS_PROBE_FAIL"
	CategoryDBLock             = "DB_LOCK"
	CategoryNoFault            = "NO_FAULT"
)

// Model defines the interface for language and reasoning backends.
// It decouples AnalyzerAgent from concrete API clients (Gemini, heuristics, recordings).
type Model interface {
	Analyze(ctx context.Context, invID, prompt string) (*AnalysisResult, error)
	SendToolResults(ctx context.Context, invID string, r []protocol.ToolCallResult) (*AnalysisResult, error)
}

// Forgetter is implemented by models that keep per-investigation state.
// Call Forget when an investigation is finished to release that state.
type Forgetter interface{ Forget(invID string) }
