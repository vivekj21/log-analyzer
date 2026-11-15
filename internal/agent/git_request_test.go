package agent

import (
	"strings"
	"testing"

	"k8s-log-analyzer/internal/protocol"
)

func TestBuildGitRequestUsesAIResult(t *testing.T) {
	inv := protocol.NewInvestigation("inv1", "pod stuck", "la-demo", "image-demo", "30m")
	inv.AddEvidence(protocol.FetchResponsePayload{ToolName: "list_pods", Output: "image-demo-abc ImagePullBackOff"})
	raw := "Root Cause: Failed to pull container image from registry.\nROOT_CAUSE_CATEGORY: IMAGE_PULL_FAIL\n"
	req := buildGitRequest(inv, "", raw, "report", "/tmp/x", false)
	if req.Severity != "P2" || !strings.Contains(req.RootCause, "pull") {
		t.Fatalf("severity=%q rootcause=%q", req.Severity, req.RootCause)
	}
	if len(req.ErrorSignatures) != 1 || req.ErrorSignatures[0] != "IMAGE_PULL_FAIL" {
		t.Fatalf("signatures=%v", req.ErrorSignatures)
	}
	if req.CreateBranch {
		t.Fatal("CreateBranch must follow the flag (false)")
	}
}

func TestBuildGitRequestFallbackWithoutAI(t *testing.T) {
	inv := protocol.NewInvestigation("inv2", "s", "ns", "svc", "1h")
	req := buildGitRequest(inv, "Partial Investigation", "", "r", ".", true)
	if req.Severity != "" || len(req.ErrorSignatures) != 0 || !req.CreateBranch {
		t.Fatalf("unexpected %+v", req)
	}
}

func TestFilterEventsForService(t *testing.T) {
	in := "NAMESPACE LAST SEEN TYPE REASON OBJECT MESSAGE\nla-demo 1m Warning Failed pod/image-demo-abc ErrImagePull\nla-demo 1m Warning BackOff pod/config-demo-xyz Back-off\n"
	out := filterEventsForService(in, "config-demo")
	if !strings.Contains(out, "config-demo-xyz") || strings.Contains(out, "image-demo") || !strings.Contains(out, "NAMESPACE") {
		t.Fatalf("bad filter:\n%s", out)
	}
	if filterEventsForService(in, "") != in {
		t.Fatal("empty service must not filter")
	}
}
