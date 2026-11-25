package agent

import (
	"context"
	"testing"

	"k8s-log-analyzer/internal/llm"
	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/rootcause"
	"k8s-log-analyzer/internal/toolbox"
)

// A message with the wrong payload type must be ignored, not crash the whole program.
func TestWrongPayloadTypeIsIgnored(t *testing.T) {
	bus := protocol.NewAgentBus(4)
	wrong := protocol.Message{Type: protocol.MsgFetchResponse, InvestigationID: "x", Payload: "not a payload"}
	p := NewProducerAgent(bus, toolbox.NewToolRegistry(), make(chan string, 1), nil)
	for _, typ := range []protocol.MessageType{protocol.MsgInvestigate, protocol.MsgFetchResponse, protocol.MsgAnalyzeResponse, protocol.MsgError, protocol.MsgGitPersistResponse} {
		m := wrong
		m.Type = typ
		p.handleMessage(context.Background(), m)
	}
	f := NewFetcherAgent(bus, nil, nil)
	m := wrong
	m.Type = protocol.MsgFetchRequest
	f.handleMessage(context.Background(), m)
	a := NewAnalyzerAgent(bus, nil)
	m.Type = protocol.MsgAnalyzeRequest
	a.handleMessage(context.Background(), m)
}

// When the investigation runs out of rounds, the report still carries a category, never a blank one.
func TestPartialReportHasACategory(t *testing.T) {
	bus := protocol.NewAgentBus(4)
	p := NewProducerAgent(bus, toolbox.NewToolRegistry(), make(chan string, 1), nil)
	inv := protocol.NewInvestigation("inv-partial", "crash loop", "demo", "web", "1h")
	inv.AddAnalysis(protocol.AnalyzeResponsePayload{RawAnalysis: "still looking"})
	p.forceComplete(inv, "Maximum rounds reached.")
	req := (<-bus.GitCh).Payload.(protocol.GitPersistRequestPayload)
	if c, ok := llm.ValidCategoryLine(req.ReportMarkdown); !ok || c != llm.CategoryCannotDetermine {
		t.Fatalf("the report text must end with a valid category line, got:\n%s", req.ReportMarkdown)
	}
	if len(req.ErrorSignatures) != 1 || req.ErrorSignatures[0] != llm.CategoryCannotDetermine || req.Severity != rootcause.Severity(llm.CategoryCannotDetermine) {
		t.Fatalf("category and severity must be recorded: %+v", req)
	}
}
