package agent

import (
	"context"
	"log"

	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/redact"
	"k8s-log-analyzer/internal/toolbox"
)

type FetcherAgent struct {
	bus      *protocol.AgentBus
	executor toolbox.ToolExecutor
	policy   *toolbox.Policy

	// investigations maps investigation IDs to their scope so the policy
	// can enforce namespace confinement.
	investigations map[string]toolbox.InvestigationScope
}

func NewFetcherAgent(bus *protocol.AgentBus, executor toolbox.ToolExecutor, policy *toolbox.Policy) *FetcherAgent {
	return &FetcherAgent{
		bus:            bus,
		executor:       executor,
		policy:         policy,
		investigations: make(map[string]toolbox.InvestigationScope),
	}
}

// RegisterInvestigation records the scope (namespace) for a new
// investigation so the policy can enforce namespace confinement.
func (f *FetcherAgent) RegisterInvestigation(id string, scope toolbox.InvestigationScope) {
	f.investigations[id] = scope
}

func (f *FetcherAgent) Run(ctx context.Context) {
	log.Println("[Fetcher] ▶ Agent started — awaiting tool requests")
	for {
		select {
		case <-ctx.Done():
			log.Println("[Fetcher] ■ Agent shutting down")
			return
		case msg := <-f.bus.FetcherCh:
			f.handleMessage(ctx, msg)
		}
	}
}

func (f *FetcherAgent) handleMessage(ctx context.Context, msg protocol.Message) {
	if msg.Type != protocol.MsgFetchRequest {
		return
	}

	payload := msg.Payload.(protocol.FetchRequestPayload)

	// --- Policy check (before any execution) ---
	scope := f.investigations[msg.InvestigationID]
	if f.policy != nil {
		if denial, err := f.policy.Check(msg.InvestigationID, scope, payload.ToolName, payload.Parameters); err != nil {
			// Return the denial as the tool result so the model can adapt.
			f.bus.ProducerCh <- protocol.NewMessage(
				protocol.MsgFetchResponse,
				msg.InvestigationID, "fetcher", "producer",
				protocol.FetchResponsePayload{
					ToolName: payload.ToolName,
					Output:   denial,
					ExitCode: -1,
				},
			)
			return
		}
	}

	result, err := f.executor.Execute(ctx, payload.ToolName, payload.Parameters)

	response := protocol.FetchResponsePayload{ToolName: payload.ToolName}
	if err != nil {
		response.Error = err.Error()
		response.ExitCode = -1
		if result != nil {
			response.Duration = result.Duration
		}
	} else {
		// Redact secrets from tool output BEFORE it becomes evidence.
		response.Output = redact.Redact(result.Stdout)
		response.Error = redact.Redact(result.Error)
		response.ExitCode = result.ExitCode
		response.Duration = result.Duration
	}

	f.bus.ProducerCh <- protocol.NewMessage(
		protocol.MsgFetchResponse,
		msg.InvestigationID, "fetcher", "producer",
		response,
	)
}
