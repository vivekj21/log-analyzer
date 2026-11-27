package llm

import (
	"context"
	"strings"
	"testing"

	"k8s-log-analyzer/internal/protocol"
)

const oldPullEvent = "3m  Warning  Failed  pod/web-abc  Failed to pull image \"busybox:1.36\": rpc error: toomanyrequests\n"

func healthyTable(status, ready string) string {
	return "NAME      READY   STATUS    RESTARTS   AGE\nweb-abc   " + ready + "   " + status + "   0   5m\n"
}

// run feeds a prompt, then (if the model asks for tools) a tool result, and returns the final report.
func runHeuristic(t *testing.T, prompt, toolOutput string) string {
	t.Helper()
	h := NewHeuristicModel()
	res, err := h.Analyze(context.Background(), "inv", prompt)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasToolCalls {
		return res.TextResponse
	}
	res, err = h.SendToolResults(context.Background(), "inv", []protocol.ToolCallResult{{ToolName: "describe_pod", Result: toolOutput}})
	if err != nil {
		t.Fatal(err)
	}
	return res.TextResponse
}

// An old pull event must not condemn a pod that is running now (both in the prompt and in the later tool output).
func TestHeuristic_OldPullEventOnHealthyPod(t *testing.T) {
	prompt := "symptom: slow\n" + healthyTable("Running", "1/1") + "\nEvents:\n" + oldPullEvent
	for _, tool := range []string{"", "Events:\n" + oldPullEvent} {
		report := runHeuristic(t, prompt, tool)
		if strings.Contains(report, "IMAGE_PULL_FAIL") {
			t.Errorf("healthy pod with an old pull event was called IMAGE_PULL_FAIL (tool output %q):\n%s", tool, report)
		}
	}
}

func TestHeuristic_PodWaitingOnImageIsImagePullFail(t *testing.T) {
	for _, status := range []string{"ErrImagePull", "ImagePullBackOff", "Init:ImagePullBackOff", "InvalidImageName"} {
		report := runHeuristic(t, "symptom: down\n"+healthyTable(status, "0/1")+"\nEvents:\n"+oldPullEvent, "")
		if !strings.Contains(report, "ROOT_CAUSE_CATEGORY: IMAGE_PULL_FAIL") {
			t.Errorf("status %s: want IMAGE_PULL_FAIL, got:\n%s", status, report)
		}
	}
}

// A pod stuck in ImagePullBackOff must be found even when the evidence arrives later as tool output.
func TestHeuristic_PullWaitStaysKnownAcrossToolCall(t *testing.T) {
	prompt := "symptom: down\n" + healthyTable("Pending", "0/1")
	report := runHeuristic(t, prompt, "Events:\n"+oldPullEvent)
	// table shows Pending, not a pull status: the old event alone is not enough
	if strings.Contains(report, "IMAGE_PULL_FAIL") {
		t.Errorf("Pending pod with only an old pull event must not be IMAGE_PULL_FAIL:\n%s", report)
	}
}

// No pod table (a plain log file): the words alone decide, as before.
func TestHeuristic_NoTableKeepsOldBehaviour(t *testing.T) {
	report := runHeuristic(t, "2026-01-01 worker: Failed to pull image \"x:1\"", "")
	if !strings.Contains(report, "ROOT_CAUSE_CATEGORY: IMAGE_PULL_FAIL") {
		t.Errorf("no table: want IMAGE_PULL_FAIL, got:\n%s", report)
	}
}

// The report shows which line made the rule fire.
func TestHeuristic_ReportShowsMatchedLine(t *testing.T) {
	report := runHeuristic(t, "symptom: down\n"+healthyTable("ErrImagePull", "0/1"), "")
	if !strings.Contains(report, "- Matched heuristic pattern: `web-abc 0/1 ErrImagePull 0 5m`") {
		t.Errorf("evidence line missing:\n%s", report)
	}
	report = runHeuristic(t, "payment pod terminated with OOMKilled\nnext line", "")
	if !strings.Contains(report, "- Matched heuristic pattern: `payment pod terminated with OOMKilled`\n") || strings.Contains(report, "next line") {
		t.Errorf("evidence must be just the matched line:\n%s", report)
	}
}

func TestHeuristic_EvidenceLineIsCut(t *testing.T) {
	long := "OOMKilled " + strings.Repeat("x", 500)
	if got := lineWith(long, "oomkilled"); len([]rune(got)) > 203 {
		t.Errorf("evidence line not cut: %d characters", len([]rune(got)))
	}
}
