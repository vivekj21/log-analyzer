package agent

import (
	"strings"
	"testing"
	"time"

	"k8s-log-analyzer/internal/protocol"
)

func invWith(output string) *protocol.Investigation {
	inv := protocol.NewInvestigation("inv-t", "pods restart", "shop", "checkout", "1h")
	inv.AddEvidence(protocol.FetchResponsePayload{ToolName: "describe_pod", Output: output})
	inv.Complete()
	inv.CompletedAt = inv.StartedAt.Add(5 * time.Second)
	return inv
}

// A rule-based answer quotes a line that came from the tool output, so it must pass the check.
func TestBuildReportKeepsAnswerWhoseEvidenceIsReal(t *testing.T) {
	inv := invWith("Name: checkout\n  Reason:   OOMKilled\n  Exit Code: 137\n")
	raw := "Root Cause: memory limit.\nConfidence: HIGH\nEvidence:\n- Matched heuristic pattern: `Reason: OOMKilled`\n\nROOT_CAUSE_CATEGORY: OOM_KILLED\n"
	rep := buildReport(inv, protocol.AnalyzeResponsePayload{RawAnalysis: raw})
	if rep.Category != "OOM_KILLED" || rep.EvidenceCheck.Result != "verified" || rep.DurationSeconds != 5 {
		t.Fatalf("%+v", rep)
	}
}

// A model that quotes lines the tools never returned must not get its fault category through.
func TestBuildReportRejectsAnswerWhoseEvidenceWasInvented(t *testing.T) {
	inv := invWith("Name: checkout\n  Reason:   Completed\n")
	raw := "Root Cause: db lock.\nConfidence: HIGH\nEvidence:\n- describe_pod: `deadlock detected on table orders`\n\nROOT_CAUSE_CATEGORY: DB_LOCK\n"
	rep := buildReport(inv, protocol.AnalyzeResponsePayload{RawAnalysis: raw})
	if rep.Category != "CANNOT_DETERMINE" || !strings.HasSuffix(strings.TrimSpace(rep.Markdown()), "ROOT_CAUSE_CATEGORY: CANNOT_DETERMINE") {
		t.Fatalf("%+v", rep)
	}
}
