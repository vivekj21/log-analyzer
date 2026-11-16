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

// OpenAIClient talks to any service that offers the OpenAI "chat completions" API with tools.
type OpenAIClient struct {
	caller  *httpCaller
	cfg     ProviderConfig
	url     string
	headers map[string]string
	tools   []map[string]any
	convos  *convoStore[oaMessage]
	budget  *Budget
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaResponse struct {
	Choices []struct {
		Message      oaMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int64 `json:"total_tokens"`
	} `json:"usage"`
}

func strp(s string) *string { return &s }

func newOpenAIClient(cfg ProviderConfig, registry *toolbox.ToolRegistry) *OpenAIClient {
	var tools []map[string]any
	for _, t := range toolSchemas(registry.AllTools()) {
		tools = append(tools, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Parameters},
		})
	}
	c := &OpenAIClient{
		caller:  newHTTPCaller("OpenAI-compatible", cfg.APIKey),
		cfg:     cfg,
		url:     cfg.baseURL() + "/chat/completions",
		headers: map[string]string{"Authorization": "Bearer " + cfg.APIKey},
		tools:   tools,
		convos:  newConvoStore[oaMessage](),
		budget:  NewBudgetFromEnv(),
	}
	log.Printf("[OpenAI-compatible] Client initialized: model=%s, server=%s, tools=%d", cfg.Model, cfg.baseURL(), len(tools))
	return c
}

func (c *OpenAIClient) Close() {}

func (c *OpenAIClient) Forget(id string) {
	c.convos.forget(id)
	if c.budget != nil {
		c.budget.Forget(id)
	}
}

func (c *OpenAIClient) Analyze(ctx context.Context, invID, prompt string) (*AnalysisResult, error) {
	var msgs []oaMessage
	c.convos.with(invID, func(cv *convo[oaMessage]) {
		cv.msgs = append(cv.msgs, oaMessage{Role: "user", Content: strp(prompt)})
		cv.pending = nil
		msgs = append([]oaMessage(nil), cv.msgs...)
	})
	log.Printf("[OpenAI-compatible] Sending prompt for investigation %s (%d chars)", invID, len(prompt))
	return c.step(ctx, invID, msgs)
}

func (c *OpenAIClient) SendToolResults(ctx context.Context, invID string, results []protocol.ToolCallResult) (*AnalysisResult, error) {
	outs := make([]toolOutput, 0, len(results))
	for _, r := range results {
		outs = append(outs, toolOutputText(r.ToolName, r.Result, r.Error))
	}
	var msgs []oaMessage
	c.convos.with(invID, func(cv *convo[oaMessage]) {
		paired, unmatched, unanswered := matchResults(cv.pending, outs)
		for _, p := range paired {
			cv.msgs = append(cv.msgs, oaMessage{Role: "tool", ToolCallID: p.call.id, Content: strp(p.out.text)})
		}
		for _, p := range unanswered { // every call needs an answer or the API rejects the conversation
			cv.msgs = append(cv.msgs, oaMessage{Role: "tool", ToolCallID: p.id, Content: strp("ERROR: this call was not executed")})
		}
		for _, u := range unmatched { // a result with no matching call goes in as plain text
			cv.msgs = append(cv.msgs, oaMessage{Role: "user", Content: strp(fmt.Sprintf("Tool result for %s:\n%s", u.name, u.text))})
		}
		cv.pending = nil
		msgs = append([]oaMessage(nil), cv.msgs...)
	})
	log.Printf("[OpenAI-compatible] Sending %d tool results for investigation %s", len(results), invID)
	return c.step(ctx, invID, msgs)
}

func (c *OpenAIClient) step(ctx context.Context, invID string, msgs []oaMessage) (*AnalysisResult, error) {
	all := append([]oaMessage{{Role: "system", Content: strp(systemPrompt + UntrustedDataInstruction)}}, msgs...)
	body := map[string]any{"model": c.cfg.Model, "messages": all}
	if len(c.tools) > 0 {
		body["tools"] = c.tools
	}
	var parsed oaResponse
	err := guardedCall(c.budget, invID, func() (int64, error) {
		raw, err := c.caller.post(ctx, c.url, c.headers, body)
		if err != nil {
			return 0, err
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return 0, fmt.Errorf("the reply was not valid JSON: %s", c.caller.scrub(excerpt(raw)))
		}
		return parsed.Usage.TotalTokens, nil
	})
	if err != nil {
		return nil, fmt.Errorf("model call failed: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("empty response from the model")
	}
	ch := parsed.Choices[0]
	res := &AnalysisResult{TotalTokens: parsed.Usage.TotalTokens}
	var pending []pendingCall
	for _, tc := range ch.Message.ToolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			args = map[string]any{} // the tool will report the missing arguments back to the model
		}
		fc := FunctionCallRequest{Name: tc.Function.Name, Args: argsToStrings(args)}
		res.FunctionCalls = append(res.FunctionCalls, fc)
		res.HasToolCalls = true
		pending = append(pending, pendingCall{id: tc.ID, name: tc.Function.Name})
		log.Printf("[OpenAI-compatible] Tool invoked: %s(%v)", fc.Name, fc.Args)
	}
	text := ""
	if ch.Message.Content != nil {
		text = strings.TrimSpace(*ch.Message.Content)
	}
	if !res.HasToolCalls {
		if text == "" {
			return nil, fmt.Errorf("the model returned neither text nor tool calls (finish_reason=%q)", ch.FinishReason)
		}
		res.TextResponse = text
	}
	c.convos.with(invID, func(cv *convo[oaMessage]) {
		cv.msgs = append(cv.msgs, ch.Message)
		cv.pending = pending
	})
	return res, nil
}
