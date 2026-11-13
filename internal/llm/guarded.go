package llm

import (
	"context"

	"google.golang.org/genai"
)

// generate applies the budget around the retrying Gemini call.
func (c *Client) generate(ctx context.Context, invID string, history []*genai.Content) (*genai.GenerateContentResponse, error) {
	if c.budget != nil {
		if err := c.budget.Allow(invID); err != nil {
			return nil, err
		}
	}
	resp, err := c.generateWithRetry(ctx, history)
	if c.budget != nil {
		switch {
		case err != nil && ctx.Err() == nil:
			c.budget.Fail()
		case err == nil:
			c.budget.Succeed()
			if resp != nil && resp.UsageMetadata != nil {
				c.budget.Record(invID, int64(resp.UsageMetadata.TotalTokenCount))
			}
		}
	}
	return resp, err
}
