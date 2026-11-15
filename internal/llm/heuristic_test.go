package llm

import (
	"context"
	"strings"
	"testing"

	"k8s-log-analyzer/internal/protocol"
)

func TestHeuristicModel_Categories(t *testing.T) {
	tests := []struct {
		name             string
		prompt           string
		toolResults      []protocol.ToolCallResult
		expectedCategory string
	}{
		{
			name:             "OOM killed in prompt",
			prompt:           "payment pod terminated with exit code 137 and OOMKilled",
			expectedCategory: CategoryOOMKilled,
		},
		{
			name:             "Image pull failure",
			prompt:           "orders pod has status ImagePullBackOff manifest unknown",
			expectedCategory: CategoryImagePullFail,
		},
		{
			name:             "Database deadlock in tool result",
			prompt:           "billing pod is running but API requests failing\n" + podTable,
			toolResults:      []protocol.ToolCallResult{{ToolName: "get_pod_logs", Result: "ERROR: deadlock detected; Process 14 waits for ExclusiveLock"}},
			expectedCategory: CategoryDBLock,
		},
		{
			name:             "Readiness probe failure",
			prompt:           "pod in CrashLoopBackOff: Readiness probe failed: HTTP 503",
			expectedCategory: CategoryReadinessProbeFail,
		},
		{
			name:             "Bad configuration panic",
			prompt:           "CrashLoopBackOff\n" + podTable,
			toolResults:      []protocol.ToolCallResult{{ToolName: "get_pod_logs", Result: "panic: missing required configuration environment variable: DB_URL"}},
			expectedCategory: CategoryCrashLoopBadConfig,
		},
		{
			name:             "Healthy no fault",
			prompt:           "No events found in default namespace. Pods running 200 OK",
			expectedCategory: CategoryNoFault,
		},
	}

	h := NewHeuristicModel()
	ctx := context.Background()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := h.Analyze(ctx, "case-1", tc.prompt)
			if err != nil {
				t.Fatalf("Analyze error: %v", err)
			}

			var finalReport string
			if res.HasToolCalls {
				followUp, err := h.SendToolResults(ctx, "case-1", tc.toolResults)
				if err != nil {
					t.Fatalf("SendToolResults error: %v", err)
				}
				finalReport = followUp.TextResponse
			} else {
				finalReport = res.TextResponse
			}

			expectedTag := "ROOT_CAUSE_CATEGORY: " + tc.expectedCategory
			if !strings.Contains(finalReport, expectedTag) {
				t.Errorf("expected report to contain %q, got:\n%s", expectedTag, finalReport)
			}
		})
	}
}

// podTable is what "kubectl get pods" prints; the heuristic reads the pod name from it.
const podTable = "NAME                       READY   STATUS             RESTARTS   AGE\n" +
	"billing-7d9f8c6b5-abcde    0/1     CrashLoopBackOff   4          5m\n"

// With nothing to work from, the rule-based model must say "cannot tell", never "healthy".
func TestHeuristicModel_NoDataIsNotHealthy(t *testing.T) {
	h := NewHeuristicModel()
	ctx := context.Background()

	// 1. Symptom mentions a crash but there is no pod table to pick a pod from.
	res, err := h.Analyze(ctx, "n1", "pod in CrashLoopBackOff right after deploy")
	if err != nil {
		t.Fatal(err)
	}
	if res.HasToolCalls || !strings.Contains(res.TextResponse, "ROOT_CAUSE_CATEGORY: CANNOT_DETERMINE") {
		t.Errorf("no pod table: want CANNOT_DETERMINE, got:\n%s", res.TextResponse)
	}

	// 2. Tool output with no known pattern.
	res, err = h.SendToolResults(ctx, "n2", []protocol.ToolCallResult{{ToolName: "get_pod_logs", Result: "all quiet"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.TextResponse, "ROOT_CAUSE_CATEGORY: CANNOT_DETERMINE") {
		t.Errorf("unknown logs: want CANNOT_DETERMINE, got:\n%s", res.TextResponse)
	}
	for _, r := range []string{res.TextResponse} {
		if strings.Contains(r, "NO_FAULT") || strings.Contains(r, "HIGH") || strings.Contains(r, "healthy") {
			t.Errorf("answer must not claim healthy or high confidence:\n%s", r)
		}
	}
}
