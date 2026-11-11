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
			prompt:           "billing pod is running but API requests failing",
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
			prompt:           "CrashLoopBackOff",
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
