package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"k8s-log-analyzer/internal/llm"
	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/report"
	"k8s-log-analyzer/internal/toolbox"
)

const MaxFollowUpRounds = 10

type ProducerAgent struct {
	bus            *protocol.AgentBus
	registry       *toolbox.ToolRegistry
	investigations map[string]*protocol.Investigation
	followUpCounts map[string]int
	reportCh       chan<- string
	enableGit      bool
	repoDir        string
	createBranch   bool
	onFinish       func(string)
	onResult       func(Result)
	fetcher        *FetcherAgent
	mu             sync.Mutex
}

func NewProducerAgent(bus *protocol.AgentBus, registry *toolbox.ToolRegistry, reportCh chan<- string, fetcher *FetcherAgent) *ProducerAgent {
	return &ProducerAgent{
		bus:            bus,
		registry:       registry,
		investigations: make(map[string]*protocol.Investigation),
		followUpCounts: make(map[string]int),
		reportCh:       reportCh,
		enableGit:      true,
		repoDir:        ".",
		fetcher:        fetcher,
	}
}

// SetGitPersistence configures whether final reports are committed into Git.
func (p *ProducerAgent) SetGitPersistence(enable bool, repoDir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enableGit = enable
	if repoDir != "" {
		p.repoDir = repoDir
	}
}

func (p *ProducerAgent) Run(ctx context.Context) {
	log.Println("[Producer] ▶ Agent started — orchestrating investigations")
	for {
		select {
		case <-ctx.Done():
			log.Println("[Producer] ■ Agent shutting down")
			return
		case msg := <-p.bus.ProducerCh:
			p.handleMessage(ctx, msg)
		}
	}
}

func (p *ProducerAgent) handleMessage(ctx context.Context, msg protocol.Message) {
	switch msg.Type {
	case protocol.MsgInvestigate:
		p.startInvestigation(ctx, msg)
	case protocol.MsgFetchResponse:
		p.handleFetchResponse(ctx, msg)
	case protocol.MsgAnalyzeResponse:
		p.handleAnalyzeResponse(ctx, msg)
	case protocol.MsgGitPersistResponse:
		p.handleGitPersistResponse(ctx, msg)
	case protocol.MsgError:
		p.handleError(ctx, msg)
	}
}

func (p *ProducerAgent) startInvestigation(ctx context.Context, msg protocol.Message) {
	payload, ok := msg.Payload.(protocol.InvestigatePayload)
	if !ok {
		log.Printf("[Agent] Ignoring a message with the wrong payload type (%T) for investigation %s", msg.Payload, msg.InvestigationID)
		return
	}
	inv := protocol.NewInvestigation(msg.InvestigationID, payload.Symptom, payload.Namespace, payload.Service, payload.Since)

	p.mu.Lock()
	p.investigations[inv.ID] = inv
	p.followUpCounts[inv.ID] = 0
	p.mu.Unlock()

	// Register the investigation scope with the Fetcher so that the
	// policy engine can enforce namespace confinement.
	if p.fetcher != nil {
		p.fetcher.RegisterInvestigation(inv.ID, toolbox.InvestigationScope{
			Namespace: payload.Namespace,
		})
	}

	inv.SetPhase(protocol.PhaseContextGathering)

	fetchRequests := []protocol.FetchRequestPayload{
		{ToolName: "list_pods", Parameters: map[string]string{"namespace": payload.Namespace}},
		{ToolName: "get_events", Parameters: map[string]string{"namespace": payload.Namespace}},
	}
	if payload.Service != "" {
		fetchRequests[0].Parameters["label_selector"] = "app=" + payload.Service
	}

	p.dispatchFetches(inv.ID, fetchRequests)
}

func (p *ProducerAgent) handleFetchResponse(ctx context.Context, msg protocol.Message) {
	payload, ok := msg.Payload.(protocol.FetchResponsePayload)
	if !ok {
		log.Printf("[Agent] Ignoring a message with the wrong payload type (%T) for investigation %s", msg.Payload, msg.InvestigationID)
		return
	}

	p.mu.Lock()
	inv := p.investigations[msg.InvestigationID]
	p.mu.Unlock()

	if inv == nil {
		return
	}

	if payload.ToolName == "get_events" {
		payload.Output = filterEventsForService(payload.Output, inv.Service)
	}
	inv.AddEvidence(payload)
	if !inv.AddPendingResult(payload) {
		return
	}

	results := inv.DrainPendingResults()
	phase := inv.GetPhase()

	switch phase {
	case protocol.PhaseContextGathering:
		inv.SetPhase(protocol.PhaseAnalysis)
		p.bus.AnalyzerCh <- protocol.NewMessage(
			protocol.MsgAnalyzeRequest,
			inv.ID, "producer", "analyzer",
			protocol.AnalyzeRequestPayload{Symptom: inv.Symptom, Context: inv.Evidence},
		)
	case protocol.PhaseFollowUp:
		inv.SetPhase(protocol.PhaseAnalysis)
		toolResults := make([]protocol.ToolCallResult, 0, len(results))
		for _, r := range results {
			toolResults = append(toolResults, protocol.ToolCallResult{ToolName: r.ToolName, Result: r.Output, Error: r.Error})
		}
		p.bus.AnalyzerCh <- protocol.NewMessage(
			protocol.MsgAnalyzeRequest,
			inv.ID, "producer", "analyzer",
			protocol.AnalyzeRequestPayload{IsFollowUp: true, ToolResults: toolResults},
		)
	}
}

func (p *ProducerAgent) handleAnalyzeResponse(ctx context.Context, msg protocol.Message) {
	payload, ok := msg.Payload.(protocol.AnalyzeResponsePayload)
	if !ok {
		log.Printf("[Agent] Ignoring a message with the wrong payload type (%T) for investigation %s", msg.Payload, msg.InvestigationID)
		return
	}

	p.mu.Lock()
	inv := p.investigations[msg.InvestigationID]
	rounds := p.followUpCounts[msg.InvestigationID]
	p.mu.Unlock()

	if inv == nil {
		return
	}

	inv.AddAnalysis(payload)

	if payload.NeedsMoreData {
		if rounds >= MaxFollowUpRounds {
			p.forceComplete(inv, "Maximum rounds reached. Providing partial root cause report.")
			return
		}

		p.mu.Lock()
		p.followUpCounts[msg.InvestigationID] = rounds + 1
		p.mu.Unlock()

		inv.SetPhase(protocol.PhaseFollowUp)
		p.dispatchFetches(inv.ID, payload.ToolCalls)
		return
	}

	inv.Complete()
	rep := buildReport(inv, payload)
	md := rep.Markdown()
	inv.FinalReport = md

	if p.enableGit {
		// The Git commit must carry the checked category and summary, not the model's raw claim.
		p.persistToGit(inv, rep.Summary, "ROOT_CAUSE_CATEGORY: "+rep.Category, md)
		return
	}

	p.finish(inv.ID)
	p.deliver(Result{ID: inv.ID, Status: StatusDone, Markdown: md, Report: &rep}, md)
}

func (p *ProducerAgent) handleError(ctx context.Context, msg protocol.Message) {
	payload, ok := msg.Payload.(protocol.ErrorPayload)
	if !ok {
		log.Printf("[Agent] Ignoring a message with the wrong payload type (%T) for investigation %s", msg.Payload, msg.InvestigationID)
		return
	}
	p.mu.Lock()
	inv := p.investigations[msg.InvestigationID]
	p.mu.Unlock()

	if inv != nil {
		inv.Fail()
		p.finish(inv.ID)
		text := fmt.Sprintf("Investigation Failed: [%s] %s\n", payload.Code, payload.Message)
		p.deliver(Result{ID: inv.ID, Status: StatusFailed, Markdown: text, Error: fmt.Sprintf("[%s] %s", payload.Code, payload.Message)}, text)
	}
}

func (p *ProducerAgent) dispatchFetches(investigationID string, requests []protocol.FetchRequestPayload) {
	p.mu.Lock()
	inv := p.investigations[investigationID]
	p.mu.Unlock()

	if inv == nil {
		return
	}

	requests = withNamespace(requests, inv.Namespace, p.registry)
	inv.SetPendingFetches(len(requests))
	for _, req := range requests {
		p.bus.FetcherCh <- protocol.NewMessage(protocol.MsgFetchRequest, investigationID, "producer", "fetcher", req)
	}
}

func (p *ProducerAgent) forceComplete(inv *protocol.Investigation, reason string) {
	inv.Complete()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n=== ROOT CAUSE REPORT (Partial: %s) ===\n\n", reason))
	for i, a := range inv.Analyses {
		if a.RawAnalysis != "" {
			sb.WriteString(fmt.Sprintf("Round %d Analysis:\n%s\n\n", i+1, a.RawAnalysis))
		}
	}
	sb.WriteString("ROOT_CAUSE_CATEGORY: " + llm.CategoryCannotDetermine + "\n")
	inv.FinalReport = sb.String()

	if p.enableGit {
		// The investigation did not finish, so the category is CANNOT_DETERMINE, never blank.
		p.persistToGit(inv, "Partial Investigation", "ROOT_CAUSE_CATEGORY: "+llm.CategoryCannotDetermine, inv.FinalReport)
		return
	}

	p.finish(inv.ID)
	pr := partialReport(inv, reason)
	p.deliver(Result{ID: inv.ID, Status: StatusPartial, Markdown: inv.FinalReport, Report: &pr}, inv.FinalReport)
}

func (p *ProducerAgent) persistToGit(inv *protocol.Investigation, rootCause, raw, report string) {
	p.mu.Lock()
	createBranch := p.createBranch
	p.mu.Unlock()
	p.bus.GitCh <- protocol.NewMessage(protocol.MsgGitPersistRequest, inv.ID, "producer", "git",
		buildGitRequest(inv, rootCause, raw, report, p.repoDir, createBranch))
}

func (p *ProducerAgent) handleGitPersistResponse(ctx context.Context, msg protocol.Message) {
	payload, ok := msg.Payload.(protocol.GitPersistResponsePayload)
	if !ok {
		log.Printf("[Agent] Ignoring a message with the wrong payload type (%T) for investigation %s", msg.Payload, msg.InvestigationID)
		return
	}

	p.mu.Lock()
	inv := p.investigations[msg.InvestigationID]
	p.mu.Unlock()

	if inv == nil {
		return
	}

	if payload.Success {
		inv.GitCommit = payload.CommitHash
		inv.GitFilePath = payload.FilePath
		inv.FinalReport = fmt.Sprintf("%s\n\n[GitOps] ✔ Recorded & committed incident to Git:\n  • File:   %s\n  • Commit: %s\n  • Branch: %s\n",
			inv.FinalReport, payload.FilePath, payload.CommitHash, payload.Branch)
	} else {
		inv.FinalReport = fmt.Sprintf("%s\n\n[GitOps] ⚠ Git persistence failed: %s\n", inv.FinalReport, payload.Error)
	}

	p.finish(inv.ID)
	p.deliver(Result{ID: inv.ID, Status: StatusDone, Markdown: inv.FinalReport}, inv.FinalReport)
}

// Result statuses.
const (
	StatusDone    = "done"    // the investigation finished and produced a report
	StatusPartial = "partial" // it ran out of rounds; the report says it cannot tell
	StatusFailed  = "failed"  // a tool or the model failed; there is no report
)

// Result is how an investigation ended. The command line prints the text; the REST service
// (internal/serve) uses the structured report.
type Result struct {
	ID       string
	Status   string
	Markdown string
	Report   *report.Report // nil when Status is failed, or when Git is on
	Error    string         // set when Status is failed
}

// SetResultHandler sends every finished investigation to fn instead of the report channel.
func (p *ProducerAgent) SetResultHandler(fn func(Result)) {
	p.mu.Lock()
	p.onResult = fn
	p.mu.Unlock()
}

// deliver hands a finished investigation to the result handler if there is one, else to the
// report channel the command line reads.
func (p *ProducerAgent) deliver(r Result, text string) {
	p.mu.Lock()
	fn := p.onResult
	p.mu.Unlock()
	if fn != nil {
		fn(r)
		return
	}
	p.reportCh <- text
}

// Cancel stops an investigation that is still running (for example, one that timed out).
// Later messages for it are ignored, and its state is released.
func (p *ProducerAgent) Cancel(id string) {
	p.mu.Lock()
	inv := p.investigations[id]
	p.mu.Unlock()
	if inv == nil {
		return
	}
	inv.Fail()
	p.finish(id)
}

// partialReport is the structured form of a run that did not finish.
func partialReport(inv *protocol.Investigation, reason string) report.Report {
	return report.Report{
		ID: inv.ID, Namespace: inv.Namespace, Service: inv.Service, Symptom: inv.Symptom,
		Category: llm.CategoryCannotDetermine, Confidence: "LOW",
		Summary:       "Cannot tell. The investigation did not finish: " + reason,
		Evidence:      []report.Evidence{},
		EvidenceCheck: report.Check{Result: "not_needed"},
		Notes:         []string{"The investigation stopped before it reached a conclusion."},
		ToolCalls:     len(inv.Evidence),
	}
}

// buildReport turns the model's answer into the structured report and checks its quoted
// evidence against the tool output that was collected.
func buildReport(inv *protocol.Investigation, analysis protocol.AnalyzeResponsePayload) report.Report {
	outs := make([]report.Output, 0, len(inv.Evidence))
	for _, ev := range inv.Evidence {
		outs = append(outs, report.Output{Tool: ev.ToolName, Text: ev.Output + "\n" + ev.Error})
	}
	return report.Build(analysis.RawAnalysis, report.Input{
		ID: inv.ID, Namespace: inv.Namespace, Service: inv.Service, Symptom: inv.Symptom,
		Duration: inv.CompletedAt.Sub(inv.StartedAt), Outputs: outs,
	})
}
