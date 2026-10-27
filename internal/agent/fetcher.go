package agent

import (
	"context"
	"log"

	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/toolbox"
)

type FetcherAgent struct {
	bus      *protocol.AgentBus
	executor *toolbox.SandboxedExecutor
}

func NewFetcherAgent(bus *protocol.AgentBus, executor *toolbox.SandboxedExecutor) *FetcherAgent {
	return &FetcherAgent{
		bus:      bus,
		executor: executor,
	}
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
	result, err := f.executor.Execute(ctx, payload.ToolName, payload.Parameters)

	response := protocol.FetchResponsePayload{ToolName: payload.ToolName}
	if err != nil {
		response.Error = err.Error()
		response.ExitCode = -1
		if result != nil {
			response.Duration = result.Duration
		}
	} else {
		response.Output = result.Stdout
		response.Error = result.Error
		response.ExitCode = result.ExitCode
		response.Duration = result.Duration
	}

	f.bus.ProducerCh <- protocol.NewMessage(
		protocol.MsgFetchResponse,
		msg.InvestigationID, "fetcher", "producer",
		response,
	)
}
