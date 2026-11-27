package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/toolbox"
)

// pipeline runs a real producer with a fake fetcher and a fake analyzer, so the Service is
// tested through the same messages the real agents use.
type pipeline struct {
	bus     *protocol.AgentBus
	p       *ProducerAgent
	svc     *Service
	silent  bool // the fake fetcher never answers
	failing bool // the fake fetcher answers with an error
}

func newPipeline(t *testing.T, silent, failing bool) *pipeline {
	t.Helper()
	bus := protocol.NewAgentBus(50)
	p := NewProducerAgent(bus, toolbox.NewToolRegistry(), make(chan string, 1), nil)
	p.SetGitPersistence(false, "")
	pl := &pipeline{bus: bus, p: p, silent: silent, failing: failing}
	pl.svc = NewService(bus, p)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go p.Run(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-bus.FetcherCh:
				switch {
				case pl.silent:
				case pl.failing:
					bus.ProducerCh <- protocol.NewMessage(protocol.MsgError, m.InvestigationID, "fetcher", "producer",
						protocol.ErrorPayload{Code: "TOOL_FAILED", Message: "kubectl is not installed"})
				default:
					req := m.Payload.(protocol.FetchRequestPayload)
					bus.ProducerCh <- protocol.NewMessage(protocol.MsgFetchResponse, m.InvestigationID, "fetcher", "producer",
						protocol.FetchResponsePayload{ToolName: req.ToolName, Output: "Name: web\n  Reason: OOMKilled\n"})
				}
			case m := <-bus.AnalyzerCh:
				bus.ProducerCh <- protocol.NewMessage(protocol.MsgAnalyzeResponse, m.InvestigationID, "analyzer", "producer",
					protocol.AnalyzeResponsePayload{RawAnalysis: "Root Cause: memory limit.\nConfidence: HIGH\nEvidence:\n- list_pods: `Reason: OOMKilled`\n\nROOT_CAUSE_CATEGORY: OOM_KILLED\n"})
			}
		}
	}()
	return pl
}

func TestServiceReturnsAnIndependentReportPerInvestigation(t *testing.T) {
	pl := newPipeline(t, false, false)
	var wg sync.WaitGroup
	results := make([]Result, 3)
	for i := range results {
		wg.Add(1)
		go func() { // the Service itself must be safe for overlapping calls
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			r, err := pl.svc.Investigate(ctx, string(rune('a'+i))+"-id", Request{Namespace: "ns" + string(rune('a'+i)), Symptom: "restarts", Since: "30m"})
			if err != nil {
				t.Error(err)
			}
			results[i] = r
		}()
	}
	wg.Wait()
	for i, r := range results {
		want := string(rune('a'+i)) + "-id"
		if r.ID != want || r.Status != StatusDone || r.Report == nil || r.Report.Namespace != "ns"+string(rune('a'+i)) {
			t.Fatalf("result %d: %+v", i, r)
		}
		if r.Report.Category != "OOM_KILLED" || r.Report.EvidenceCheck.Result != "verified" || !strings.Contains(r.Markdown, "Incident report: OOM_KILLED") {
			t.Fatalf("result %d: %+v", i, r.Report)
		}
	}
}

func TestServiceReportsAToolFailureAsFailed(t *testing.T) {
	pl := newPipeline(t, false, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := pl.svc.Investigate(ctx, "f-id", Request{Namespace: "shop", Symptom: "x", Since: "30m"})
	if err != nil || r.Status != StatusFailed || !strings.Contains(r.Error, "kubectl is not installed") || r.Report != nil {
		t.Fatalf("%+v %v", r, err)
	}
}

// A timed-out investigation is cancelled and its state is released, and a late answer is ignored.
func TestServiceTimeoutCancelsTheInvestigation(t *testing.T) {
	pl := newPipeline(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := pl.svc.Investigate(ctx, "slow-id", Request{Namespace: "shop", Symptom: "x", Since: "30m"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a deadline error, got %v", err)
	}
	pl.p.mu.Lock()
	left := len(pl.p.investigations)
	pl.p.mu.Unlock()
	if left != 0 {
		t.Fatalf("the cancelled investigation must be released, %d left", left)
	}
	pl.svc.mu.Lock()
	w := len(pl.svc.waiters)
	pl.svc.mu.Unlock()
	if w != 0 {
		t.Fatal("the waiter must be removed")
	}
	// a late answer for the cancelled investigation must not crash or deliver anything
	pl.bus.ProducerCh <- protocol.NewMessage(protocol.MsgFetchResponse, "slow-id", "fetcher", "producer",
		protocol.FetchResponsePayload{ToolName: "list_pods", Output: "late"})
	time.Sleep(50 * time.Millisecond)
}

func TestServiceRefusesADuplicateID(t *testing.T) {
	pl := newPipeline(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go pl.svc.Investigate(ctx, "dup", Request{Namespace: "shop", Since: "30m"})
	time.Sleep(50 * time.Millisecond)
	if _, err := pl.svc.Investigate(ctx, "dup", Request{Namespace: "shop", Since: "30m"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("got %v", err)
	}
}
