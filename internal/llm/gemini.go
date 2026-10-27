// Package llm wraps the Gemini API for multi-turn, tool-augmented reasoning
// using the modern google.golang.org/genai SDK.
package llm

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"google.golang.org/genai"

	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/toolbox"
)

const systemPrompt = `You are an expert Site Reliability Engineer (SRE) and Kubernetes specialist performing autonomous root cause analysis on a production microservice incident.

## Your Investigation Process
1. Review the initial evidence (pod statuses, events, logs) carefully.
2. Form hypotheses about the root cause.
3. Use the available tools to gather MORE evidence — do not guess when you can verify.
4. When you have sufficient evidence, provide your final analysis.

## Investigation Strategy
- Start broad (list pods, check events) then narrow down (specific pod logs, describe pod).
- Check for common failure patterns:
  • CrashLoopBackOff — check previous container logs and events
  • OOMKilled — check resource usage and limits
  • ImagePullBackOff — check events for registry errors
  • Pending pods — check events for scheduling failures and node conditions
  • Connection errors — check service endpoints and dependent services
  • Configuration errors — check configmaps and environment variables
- Correlate timestamps between events and log entries.
- Consider inter-service dependencies and cascading failures.
- Aim for HIGH confidence — gather enough evidence to be sure.

## Response Format
When you have reached your conclusion, provide a clear report:

**Root Cause:** [Clear, specific description of what is causing the issue]

**Confidence:** [HIGH / MEDIUM / LOW]

**Evidence:**
- [Specific log line, event, or metric that supports the conclusion]
- [Additional evidence...]

**Recommendations:**
1. [Immediate action to resolve the issue]
2. [Preventive measure for the future]
3. [Additional recommendations...]

Do NOT provide a conclusion until you have gathered sufficient evidence. Use the tools to investigate thoroughly first.`

type AnalysisResult struct {
	FunctionCalls []FunctionCallRequest
	HasToolCalls  bool
	TextResponse  string
}

type FunctionCallRequest struct {
	Name string
	Args map[string]string
}

type Client struct {
	client    *genai.Client
	modelName string
	config    *genai.GenerateContentConfig
	histories map[string][]*genai.Content
	mu        sync.Mutex
}

func NewClient(ctx context.Context, apiKey, modelName string, registry *toolbox.ToolRegistry) (*Client, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	funcDecls := toolDefsToFuncDecls(registry.AllTools())

	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{
				genai.NewPartFromText(systemPrompt),
			},
		},
		Tools: []*genai.Tool{
			{
				FunctionDeclarations: funcDecls,
			},
		},
		ToolConfig: &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAuto,
			},
		},
	}

	log.Printf("[Gemini] Client initialized: model=%s, tools=%d", modelName, len(funcDecls))

	return &Client{
		client:    client,
		modelName: modelName,
		config:    config,
		histories: make(map[string][]*genai.Content),
	}, nil
}

func (c *Client) Close() {
	// genai.Client handles connection lifecycle automatically
}

func (c *Client) Analyze(ctx context.Context, investigationID, prompt string) (*AnalysisResult, error) {
	userContent := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{genai.NewPartFromText(prompt)},
	}

	c.mu.Lock()
	c.histories[investigationID] = append(c.histories[investigationID], userContent)
	history := make([]*genai.Content, len(c.histories[investigationID]))
	copy(history, c.histories[investigationID])
	c.mu.Unlock()

	log.Printf("[Gemini] Sending prompt for investigation %s (%d chars)", investigationID, len(prompt))

	resp, err := c.client.Models.GenerateContent(ctx, c.modelName, history, c.config)
	if err != nil {
		return nil, fmt.Errorf("Gemini GenerateContent failed: %w", err)
	}

	if resp != nil && len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		c.mu.Lock()
		c.histories[investigationID] = append(c.histories[investigationID], resp.Candidates[0].Content)
		c.mu.Unlock()
	}

	return processResponse(resp)
}

func (c *Client) SendToolResults(ctx context.Context, investigationID string, results []protocol.ToolCallResult) (*AnalysisResult, error) {
	parts := make([]*genai.Part, 0, len(results))
	for _, r := range results {
		output := r.Result
		if r.Error != "" {
			output = fmt.Sprintf("ERROR: %s\nOutput: %s", r.Error, r.Result)
		}
		parts = append(parts, genai.NewPartFromFunctionResponse(r.ToolName, map[string]any{
			"output": output,
		}))
	}

	toolContent := &genai.Content{
		Role:  "user",
		Parts: parts,
	}

	c.mu.Lock()
	c.histories[investigationID] = append(c.histories[investigationID], toolContent)
	history := make([]*genai.Content, len(c.histories[investigationID]))
	copy(history, c.histories[investigationID])
	c.mu.Unlock()

	log.Printf("[Gemini] Sending %d tool results for investigation %s", len(results), investigationID)

	resp, err := c.client.Models.GenerateContent(ctx, c.modelName, history, c.config)
	if err != nil {
		return nil, fmt.Errorf("Gemini GenerateContent (tool results) failed: %w", err)
	}

	if resp != nil && len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		c.mu.Lock()
		c.histories[investigationID] = append(c.histories[investigationID], resp.Candidates[0].Content)
		c.mu.Unlock()
	}

	return processResponse(resp)
}

func processResponse(resp *genai.GenerateContentResponse) (*AnalysisResult, error) {
	if resp == nil || len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	result := &AnalysisResult{}
	var textParts []string

	for _, cand := range resp.Candidates {
		if cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if part.FunctionCall != nil {
				fc := FunctionCallRequest{
					Name: part.FunctionCall.Name,
					Args: make(map[string]string),
				}
				for k, val := range part.FunctionCall.Args {
					fc.Args[k] = fmt.Sprintf("%v", val)
				}
				result.FunctionCalls = append(result.FunctionCalls, fc)
				result.HasToolCalls = true
				log.Printf("[Gemini] Tool invoked: %s(%v)", fc.Name, fc.Args)
			}
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		}
	}

	if !result.HasToolCalls {
		result.TextResponse = strings.Join(textParts, "\n")
	}

	return result, nil
}

func toolDefsToFuncDecls(tools []*toolbox.ToolDefinition) []*genai.FunctionDeclaration {
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))

	for _, t := range tools {
		props := make(map[string]*genai.Schema)
		var required []string

		for _, p := range t.Parameters {
			schema := &genai.Schema{
				Type:        genai.TypeString,
				Description: p.Description,
			}
			if len(p.AllowedValues) > 0 {
				schema.Enum = p.AllowedValues
			}
			props[p.Name] = schema
			if p.Required {
				required = append(required, p.Name)
			}
		}

		decls = append(decls, &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: props,
				Required:   required,
			},
		})
	}

	return decls
}
