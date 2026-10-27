package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"k8s-log-analyzer/internal/protocol"
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
	mu             sync.Mutex
}

func NewProducerAgent(bus *protocol.AgentBus, registry *toolbox.ToolRegistry, reportCh chan<- string) *ProducerAgent {
	return &ProducerAgent{
		bus:            bus,
		registry:       registry,
		investigations: make(map[string]*protocol.Investigation),
		followUpCounts: make(map[string]int),
		reportCh:       reportCh,
		enableGit:      true,
		repoDir:        ".",
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
	payload := msg.Payload.(protocol.InvestigatePayload)
	inv := protocol.NewInvestigation(msg.InvestigationID, payload.Symptom, payload.Namespace, payload.Service, payload.Since)

	p.mu.Lock()
	p.investigations[inv.ID] = inv
	p.followUpCounts[inv.ID] = 0
	p.mu.Unlock()

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
	payload := msg.Payload.(protocol.FetchResponsePayload)

	p.mu.Lock()
	inv := p.investigations[msg.InvestigationID]
	p.mu.Unlock()

	if inv == nil {
		return
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
	payload := msg.Payload.(protocol.AnalyzeResponsePayload)

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
	report := p.compileReport(inv, payload)
	inv.FinalReport = report

	if p.enableGit {
		p.persistToGit(inv, payload.RootCause, report)
		return
	}

	p.reportCh <- report
}

func (p *ProducerAgent) handleError(ctx context.Context, msg protocol.Message) {
	payload := msg.Payload.(protocol.ErrorPayload)
	p.mu.Lock()
	inv := p.investigations[msg.InvestigationID]
	p.mu.Unlock()

	if inv != nil {
		inv.Fail()
		p.reportCh <- fmt.Sprintf("Investigation Failed: [%s] %s\n", payload.Code, payload.Message)
	}
}

func (p *ProducerAgent) dispatchFetches(investigationID string, requests []protocol.FetchRequestPayload) {
	p.mu.Lock()
	inv := p.investigations[investigationID]
	p.mu.Unlock()

	if inv == nil {
		return
	}

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
	inv.FinalReport = sb.String()

	if p.enableGit {
		p.persistToGit(inv, "Partial Investigation", inv.FinalReport)
		return
	}

	p.reportCh <- inv.FinalReport
}

func (p *ProducerAgent) persistToGit(inv *protocol.Investigation, rootCause, report string) {
	var logsBuilder strings.Builder
	for _, ev := range inv.Evidence {
		if ev.Output != "" {
			logsBuilder.WriteString(fmt.Sprintf("--- [%s] ---\n%s\n\n", ev.ToolName, ev.Output))
		}
	}

	title := fmt.Sprintf("Incident in %s: %s", inv.Namespace, inv.Symptom)
	if len(title) > 60 {
		title = title[:60]
	}

	p.bus.GitCh <- protocol.NewMessage(
		protocol.MsgGitPersistRequest,
		inv.ID,
		"producer",
		"git",
		protocol.GitPersistRequestPayload{
			InvestigationID: inv.ID,
			Title:           title,
			Namespace:       inv.Namespace,
			Service:         inv.Service,
			Symptom:         inv.Symptom,
			RootCause:       rootCause,
			ReportMarkdown:  report,
			RawLogs:         logsBuilder.String(),
			RepoDir:         p.repoDir,
			CreateBranch:    true,
		},
	)
}

func (p *ProducerAgent) handleGitPersistResponse(ctx context.Context, msg protocol.Message) {
	payload := msg.Payload.(protocol.GitPersistResponsePayload)

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

	p.reportCh <- inv.FinalReport
}

func (p *ProducerAgent) compileReport(inv *protocol.Investigation, analysis protocol.AnalyzeResponsePayload) string {
	var sb strings.Builder
	sb.WriteString("\n╔══════════════════════════════════════════════════════════════════╗\n")
	sb.WriteString("║            ROOT CAUSE ANALYSIS REPORT (Gemini)                 ║\n")
	sb.WriteString("╚══════════════════════════════════════════════════════════════════╝\n\n")
	sb.WriteString(fmt.Sprintf("Investigation ID : %s\n", inv.ID))
	sb.WriteString(fmt.Sprintf("Namespace        : %s\n", inv.Namespace))
	if inv.Service != "" {
		sb.WriteString(fmt.Sprintf("Service          : %s\n", inv.Service))
	}
	sb.WriteString(fmt.Sprintf("Duration         : %s\n", inv.CompletedAt.Sub(inv.StartedAt).Round(time.Second)))
	sb.WriteString(fmt.Sprintf("Evidence Items   : %d\n\n", len(inv.Evidence)))
	sb.WriteString("─── Symptom ────────────────────────────────────────────────────────\n")
	sb.WriteString(inv.Symptom + "\n\n")
	sb.WriteString("─── Evidence Gathered ──────────────────────────────────────────────\n")
	for i, ev := range inv.Evidence {
		sb.WriteString(fmt.Sprintf("  [%d] %s (%s)\n", i+1, ev.ToolName, ev.Duration.Round(time.Millisecond)))
	}
	sb.WriteString("\n─── Analysis & Deductions ──────────────────────────────────────────\n")
	sb.WriteString(analysis.RawAnalysis + "\n")
	sb.WriteString("════════════════════════════════════════════════════════════════════\n")
	return sb.String()
}
