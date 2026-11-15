package llm

import (
	"context"
	"fmt"
	"regexp"
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

	// If a failing pod or performance symptom is detected but logs were not in discovery, request pod logs.
	lower := strings.ToLower(prompt)
	if strings.Contains(lower, "crash") || strings.Contains(lower, "error") ||
		strings.Contains(lower, "fail") || strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "latency") || strings.Contains(lower, "slow") ||
		strings.Contains(lower, "5xx") || strings.Contains(lower, "504") {
		if pod := pickPod(prompt); pod != "" {
			return &AnalysisResult{
				HasToolCalls: true,
				FunctionCalls: []FunctionCallRequest{
					{Name: "get_pod_logs", Args: map[string]string{"pod": pod, "tail_lines": "100"}},
				},
			}, nil
		}
	}

	return &AnalysisResult{TextResponse: formatHeuristicReport(CategoryNoFault, "No anomalies detected; workload appears healthy.")}, nil
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

	return &AnalysisResult{TextResponse: formatHeuristicReport(CategoryCrashLoopBadConfig, "Container crashing repeatedly without recognized signature.")}, nil
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

// podRowRegex matches a row of a "kubectl get pods" table: NAME READY STATUS.
var podRowRegex = regexp.MustCompile(`(?m)^\s*([a-z0-9][-a-z0-9.]*)\s+\d+/\d+\s+(\S+)`)

// pickPod returns the first pod that is not Running/Completed, else the first pod, else "".
func pickPod(text string) string {
	first := ""
	for _, m := range podRowRegex.FindAllStringSubmatch(text, -1) {
		if first == "" {
			first = m[1]
		}
		if m[2] != "Running" && m[2] != "Completed" {
			return m[1]
		}
	}
	return first
}
