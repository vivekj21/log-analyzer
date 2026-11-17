package llm

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s-log-analyzer/internal/protocol"
)

// podNameRegex extracts the first pod name from kubectl output.
var podNameRegex = regexp.MustCompile(`([a-zA-Z0-9][-a-zA-Z0-9]+-[a-z0-9]+-[a-z0-9]+)`)

// HeuristicModel is a rule-based, non-AI implementation of Model using strings.Contains.
type HeuristicModel struct{}

func NewHeuristicModel() *HeuristicModel {
	return &HeuristicModel{}
}

func (h *HeuristicModel) Analyze(ctx context.Context, invID, prompt string) (*AnalysisResult, error) {
	if cat, expl, ok := evaluateRules(prompt); ok {
		return &AnalysisResult{TextResponse: formatHeuristicReport(cat, expl)}, nil
	}

	// Look into a suspicious pod. A pod is suspicious if the pod table shows it is not Running,
	// not fully ready, or has restarted (this does not depend on lucky wording), or if the text
	// mentions a failure word. Logs alone often miss the cause (an out-of-memory kill is recorded
	// in the pod description, not in its logs), so read both.
	lower := strings.ToLower(prompt)
	pod, suspicious := suspectPod(prompt)
	if pod != "" && (suspicious || hasFailureWord(lower)) {
		return &AnalysisResult{
			HasToolCalls: true,
			FunctionCalls: []FunctionCallRequest{
				{Name: "describe_pod", Args: map[string]string{"pod": pod}},
				{Name: "get_pod_logs", Args: map[string]string{"pod": pod, "tail_lines": "100"}},
			},
		}, nil
	}

	return &AnalysisResult{TextResponse: formatCannotDetermine("The rule-based model found no pod list or recognizable error text to work from.")}, nil
}

func (h *HeuristicModel) SendToolResults(ctx context.Context, invID string, r []protocol.ToolCallResult) (*AnalysisResult, error) {
	var combined strings.Builder
	for _, res := range r {
		combined.WriteString(res.Result)
		combined.WriteString("\n")
		combined.WriteString(res.Error)
		combined.WriteString("\n")
	}

	if cat, expl, ok := evaluateRules(combined.String()); ok {
		return &AnalysisResult{TextResponse: formatHeuristicReport(cat, expl)}, nil
	}

	return &AnalysisResult{TextResponse: formatCannotDetermine("The rule-based model read the data but matched none of its known error patterns.")}, nil
}

func evaluateRules(text string) (string, string, bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "oomkilled") || strings.Contains(lower, "outofmemory") || strings.Contains(lower, "exit code 137") || strings.Contains(lower, "memory limit exceeded"):
		return CategoryOOMKilled, "Container exceeded memory limit and was killed (OOMKilled).", true
	case strings.Contains(lower, "imagepullbackoff") || strings.Contains(lower, "errimagepull") || strings.Contains(lower, "manifest unknown") || strings.Contains(lower, "failed to pull image"):
		return CategoryImagePullFail, "Failed to pull container image from registry.", true
	case strings.Contains(lower, "deadlock") || strings.Contains(lower, "lock wait timeout") || strings.Contains(lower, "database lock") || strings.Contains(lower, "lock contention"):
		return CategoryDBLock, "Database lock contention or deadlock detected.", true
	case strings.Contains(lower, "readiness probe failed") || strings.Contains(lower, "probe failed") || strings.Contains(lower, "unhealthy"):
		return CategoryReadinessProbeFail, "Readiness probe failed; container not marked ready.", true
	case strings.Contains(lower, "panic:") || strings.Contains(lower, "missing required configuration") || strings.Contains(lower, "configuration validation failed") || strings.Contains(lower, "syntax error") || strings.Contains(lower, "invalid configuration") || strings.Contains(lower, "required setting"):
		return CategoryCrashLoopBadConfig, "Application crashed due to bad configuration or unhandled panic.", true
	case strings.Contains(lower, "no events found") && (strings.Contains(lower, "running") || strings.Contains(lower, "200 ok")):
		return CategoryNoFault, "No fault detected; services operating normally.", true
	}
	return "", "", false
}

func formatHeuristicReport(category, explanation string) string {
	return fmt.Sprintf("Root Cause: %s\nConfidence: HIGH\nEvidence:\n- Matched heuristic pattern\n\nRecommendations:\n1. Apply standard remediation for %s.\n\nROOT_CAUSE_CATEGORY: %s\n",
		explanation, category, category)
}

// formatCannotDetermine is the honest answer when the rules have nothing to go on.
// It must not look like a diagnosis, and it must not say the workload is healthy.
func formatCannotDetermine(why string) string {
	return fmt.Sprintf("Root Cause: Cannot tell. %s\nConfidence: LOW\nEvidence:\n- No rule matched the data that was collected\n\nRecommendations:\n1. Check the pods and their logs by hand, or re-run with --llm live for a deeper look.\n\nROOT_CAUSE_CATEGORY: %s\n", why, CategoryCannotDetermine)
}

// failureWords are signs in free text that something is wrong.
var failureWords = []string{"crash", "error", "fail", "timeout", "latency", "slow", "5xx", "504", "backoff", "killed", "evict"}

func hasFailureWord(lower string) bool {
	for _, w := range failureWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// podRowRegex matches a row of a "kubectl get pods" table: NAME READY STATUS RESTARTS.
// RESTARTS may look like "3" or "3 (45s ago)"; only the number is read.
var podRowRegex = regexp.MustCompile(`(?m)^\s*([a-z0-9][-a-z0-9.]*)\s+(\d+)/(\d+)\s+(\S+)\s+(\d+)`)

// suspectPod returns the pod most worth a closer look and whether the table itself says it is
// unhealthy: not Running/Completed, not fully ready, or restarted at least once.
// With no unhealthy pod it returns the first pod (suspicious=false); with no table, "".
func suspectPod(text string) (pod string, suspicious bool) {
	first := ""
	best, bestRestarts := "", 0
	for _, m := range podRowRegex.FindAllStringSubmatch(text, -1) {
		if first == "" {
			first = m[1]
		}
		restarts, _ := strconv.Atoi(m[5])
		bad := (m[4] != "Running" && m[4] != "Completed") || (m[4] == "Running" && m[2] != m[3])
		if bad {
			return m[1], true
		}
		if restarts > bestRestarts {
			best, bestRestarts = m[1], restarts
		}
	}
	if best != "" {
		return best, true
	}
	return first, false
}

// pickPod returns the pod suspectPod chooses ("" if there is no pod table).
func pickPod(text string) string {
	pod, _ := suspectPod(text)
	return pod
}
