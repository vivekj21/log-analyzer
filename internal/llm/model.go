package llm

import (
	"context"

	"k8s-log-analyzer/internal/protocol"
)

// Root cause categories for classification and evaluation.
// This list is the single source of truth: the prompt, the severity mapping, the accuracy scripts and
// accuracy/README.md are all checked against it (go test, scripts/check-cases.sh).
const (
	CategoryOOMKilled           = "OOM_KILLED"
	CategoryCrashLoopBadConfig  = "CRASHLOOP_BAD_CONFIG"
	CategoryImagePullFail       = "IMAGE_PULL_FAIL"
	CategoryReadinessProbeFail  = "READINESS_PROBE_FAIL"
	CategoryLivenessProbeKill   = "LIVENESS_PROBE_KILL"
	CategoryDBLock              = "DB_LOCK"
	CategoryDependencyDown      = "DEPENDENCY_DOWN"
	CategoryDNSFail             = "DNS_FAIL"
	CategoryQuotaExceeded       = "QUOTA_EXCEEDED"
	CategoryRBACDenied          = "RBAC_DENIED"
	CategoryUnschedulable       = "UNSCHEDULABLE"
	CategoryStorageFail         = "STORAGE_FAIL"
	CategoryServiceMisroute     = "SERVICE_MISROUTE"
	CategoryMissingConfigObject = "MISSING_CONFIG_OBJECT"
	CategoryNoFault             = "NO_FAULT"
	CategoryCannotDetermine     = "CANNOT_DETERMINE" // not enough data to say; never means "healthy"
)

// AllCategories lists every category the model may answer with, faults first.
var AllCategories = []string{
	CategoryOOMKilled, CategoryCrashLoopBadConfig, CategoryImagePullFail, CategoryReadinessProbeFail,
	CategoryLivenessProbeKill, CategoryDBLock, CategoryDependencyDown, CategoryDNSFail, CategoryQuotaExceeded,
	CategoryRBACDenied, CategoryUnschedulable, CategoryStorageFail, CategoryServiceMisroute,
	CategoryMissingConfigObject, CategoryNoFault, CategoryCannotDetermine,
}

// Model defines the interface for language and reasoning backends.
// It decouples AnalyzerAgent from concrete API clients (Gemini, heuristics, recordings).
type Model interface {
	Analyze(ctx context.Context, invID, prompt string) (*AnalysisResult, error)
	SendToolResults(ctx context.Context, invID string, r []protocol.ToolCallResult) (*AnalysisResult, error)
}

// Forgetter is implemented by models that keep per-investigation state.
// Call Forget when an investigation is finished to release that state.
type Forgetter interface{ Forget(invID string) }
