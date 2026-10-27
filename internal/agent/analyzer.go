package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"k8s-log-analyzer/internal/llm"
	"k8s-log-analyzer/internal/protocol"
)

type AnalyzerAgent struct {
	bus    *protocol.AgentBus
	gemini *llm.Client
}

func NewAnalyzerAgent(bus *protocol.AgentBus, gemini *llm.Client) *AnalyzerAgent {
	return &AnalyzerAgent{
		bus:    bus,
		gemini: gemini,
	}
}

func (a *AnalyzerAgent) Run(ctx context.Context) {
	log.Println("[Analyzer] ▶ Agent started — awaiting analysis requests")
	for {
		select {
		case <-ctx.Done():
			log.Println("[Analyzer] ■ Agent shutting down")
			return
		case msg := <-a.bus.AnalyzerCh:
			a.handleMessage(ctx, msg)
		}
	}
}

func (a *AnalyzerAgent) handleMessage(ctx context.Context, msg protocol.Message) {
	if msg.Type != protocol.MsgAnalyzeRequest {
		return
	}

	payload := msg.Payload.(protocol.AnalyzeRequestPayload)
	var result *llm.AnalysisResult
	var err error

	if payload.IsFollowUp {
		result, err = a.gemini.SendToolResults(ctx, msg.InvestigationID, payload.ToolResults)
	} else {
		prompt := buildInvestigationPrompt(payload)
		result, err = a.gemini.Analyze(ctx, msg.InvestigationID, prompt)
	}

	if err != nil {
		a.bus.ProducerCh <- protocol.NewMessage(
			protocol.MsgError,
			msg.InvestigationID, "analyzer", "producer",
			protocol.ErrorPayload{Code: "GEMINI_ERROR", Message: err.Error()},
		)
		return
	}

	response := protocol.AnalyzeResponsePayload{}
	if result.HasToolCalls {
		response.NeedsMoreData = true
		for _, fc := range result.FunctionCalls {
			response.ToolCalls = append(response.ToolCalls, protocol.FetchRequestPayload{
				ToolName:   fc.Name,
				Parameters: fc.Args,
			})
		}
	} else {
		response.RawAnalysis = result.TextResponse
	}

	a.bus.ProducerCh <- protocol.NewMessage(
		protocol.MsgAnalyzeResponse,
		msg.InvestigationID, "analyzer", "producer",
		response,
	)
}

func buildInvestigationPrompt(payload protocol.AnalyzeRequestPayload) string {
	var sb strings.Builder
	sb.WriteString("# Kubernetes Incident Investigation\n\n")
	sb.WriteString(fmt.Sprintf("## Reported Symptom\n%s\n\n", payload.Symptom))

	if len(payload.Context) > 0 {
		sb.WriteString("## Initial Telemetry & Discovery Data\n\n")
		for i, ctx := range payload.Context {
			sb.WriteString(fmt.Sprintf("### [%d] Tool: %s\n", i+1, ctx.ToolName))
			if ctx.Output != "" {
				sb.WriteString("```\n" + ctx.Output)
				if !strings.HasSuffix(ctx.Output, "\n") {
					sb.WriteString("\n")
				}
				sb.WriteString("```\n\n")
			}
		}
	}

	sb.WriteString("Analyze the data above. If more evidence is required, invoke the available tools.\n")
	return sb.String()
}
