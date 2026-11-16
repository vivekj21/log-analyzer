package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/toolbox"
)

const (
	anthropicVersion   = "2023-06-01"
	anthropicMaxOutput = 4096
)

// AnthropicClient talks to Claude through Anthropic's Messages API.
type AnthropicClient struct {
	caller  *httpCaller
	cfg     ProviderConfig
	url     string
	headers map[string]string
	tools   []map[string]any
	convos  *convoStore[anMessage]
	budget  *Budget
}

type anMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type anResponse struct {
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

func anUser(blocks ...anBlock) anMessage {
	raw, _ := json.Marshal(blocks)
	return anMessage{Role: "user", Content: raw}
}

func newAnthropicClient(cfg ProviderConfig, registry *toolbox.ToolRegistry) *AnthropicClient {
	var tools []map[string]any
	for _, t := range toolSchemas(registry.AllTools()) {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Parameters})
	}
	c := &AnthropicClient{
		caller:  newHTTPCaller("Anthropic", cfg.APIKey),
		cfg:     cfg,
		url:     cfg.baseURL() + "/messages",
		headers: map[string]string{"x-api-key": cfg.APIKey, "anthropic-version": anthropicVersion},
		tools:   tools,
		convos:  newConvoStore[anMessage](),
		budget:  NewBudgetFromEnv(),
	}
	log.Printf("[Anthropic] Client initialized: model=%s, tools=%d", cfg.Model, len(tools))
	return c
}

func (c *AnthropicClient) Close() {}

func (c *AnthropicClient) Forget(id string) {
	c.convos.forget(id)
	if c.budget != nil {
		c.budget.Forget(id)
	}
}

func (c *AnthropicClient) Analyze(ctx context.Context, invID, prompt string) (*AnalysisResult, error) {
	var msgs []anMessage
	c.convos.with(invID, func(cv *convo[anMessage]) {
		cv.msgs = append(cv.msgs, anUser(anBlock{Type: "text", Text: prompt}))
		cv.pending = nil
		msgs = append([]anMessage(nil), cv.msgs...)
	})
	log.Printf("[Anthropic] Sending prompt for investigation %s (%d chars)", invID, len(prompt))
	return c.step(ctx, invID, msgs)
}

func (c *AnthropicClient) SendToolResults(ctx context.Context, invID string, results []protocol.ToolCallResult) (*AnalysisResult, error) {
	outs := make([]toolOutput, 0, len(results))
	for _, r := range results {
		outs = append(outs, toolOutputText(r.ToolName, r.Result, r.Error))
	}
	var msgs []anMessage
	c.convos.with(invID, func(cv *convo[anMessage]) {
		paired, unmatched, unanswered := matchResults(cv.pending, outs)
		var blocks []anBlock // all answers go into ONE user message, right after the tool_use blocks
		for _, p := range paired {
			blocks = append(blocks, anBlock{Type: "tool_result", ToolUseID: p.call.id, Content: p.out.text, IsError: p.out.isError})
		}
		for _, p := range unanswered {
			blocks = append(blocks, anBlock{Type: "tool_result", ToolUseID: p.id, Content: "ERROR: this call was not executed", IsError: true})
		}
		for _, u := range unmatched {
			blocks = append(blocks, anBlock{Type: "text", Text: fmt.Sprintf("Tool result for %s:\n%s", u.name, u.text)})
		}
		if len(blocks) > 0 {
			cv.msgs = append(cv.msgs, anUser(blocks...))
		}
		cv.pending = nil
		msgs = append([]anMessage(nil), cv.msgs...)
	})
	log.Printf("[Anthropic] Sending %d tool results for investigation %s", len(results), invID)
	return c.step(ctx, invID, msgs)
}

func (c *AnthropicClient) step(ctx context.Context, invID string, msgs []anMessage) (*AnalysisResult, error) {
	body := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": anthropicMaxOutput,
		"system":     systemPrompt + UntrustedDataInstruction,
		"messages":   msgs,
	}
	if len(c.tools) > 0 {
		body["tools"] = c.tools
	}
	var parsed anResponse
	err := guardedCall(c.budget, invID, func() (int64, error) {
		raw, err := c.caller.post(ctx, c.url, c.headers, body)
		if err != nil {
			return 0, err
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return 0, fmt.Errorf("the reply was not valid JSON: %s", c.caller.scrub(excerpt(raw)))
		}
		return parsed.Usage.InputTokens + parsed.Usage.OutputTokens, nil
	})
	if err != nil {
		return nil, fmt.Errorf("model call failed: %w", err)
	}
	var blocks []anBlock
	if err := json.Unmarshal(parsed.Content, &blocks); err != nil || len(blocks) == 0 {
		return nil, fmt.Errorf("empty response from the model (stop_reason=%q)", parsed.StopReason)
	}
	res := &AnalysisResult{TotalTokens: parsed.Usage.InputTokens + parsed.Usage.OutputTokens}
	var pending []pendingCall
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			fc := FunctionCallRequest{Name: b.Name, Args: argsToStrings(b.Input)}
			res.FunctionCalls = append(res.FunctionCalls, fc)
			res.HasToolCalls = true
			pending = append(pending, pendingCall{id: b.ID, name: b.Name})
			log.Printf("[Anthropic] Tool invoked: %s(%v)", fc.Name, fc.Args)
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				texts = append(texts, b.Text)
			}
		}
	}
	if !res.HasToolCalls {
		res.TextResponse = strings.TrimSpace(strings.Join(texts, "\n"))
		if res.TextResponse == "" {
			return nil, fmt.Errorf("the model returned neither text nor tool calls (stop_reason=%q)", parsed.StopReason)
		}
	}
	c.convos.with(invID, func(cv *convo[anMessage]) {
		cv.msgs = append(cv.msgs, anMessage{Role: "assistant", Content: parsed.Content})
		cv.pending = pending
	})
	return res, nil
}
