package llm

import (
	"context"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genai"
)

const (
	maxAttempts = 6
	maxWait     = 90 * time.Second
)

var retryInRe = regexp.MustCompile(`retry in ([0-9.]+)s`)

// retryDelay says whether an error is worth retrying and how long to wait.
// Per-minute rate limits carry a short "retry in Ns" hint and are retried.
// Daily limits ("retry in 4h21m...") and other errors are not.
func retryDelay(err error, attempt int) (time.Duration, bool) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "429") || strings.Contains(msg, "RESOURCE_EXHAUSTED"):
		m := retryInRe.FindStringSubmatch(msg)
		if m == nil {
			return 0, false
		}
		secs, perr := strconv.ParseFloat(m[1], 64)
		if perr != nil {
			return 0, false
		}
		d := time.Duration(secs*float64(time.Second)) + 2*time.Second
		if d > maxWait {
			return 0, false
		}
		return d, true
	case strings.Contains(msg, "503") || strings.Contains(msg, "UNAVAILABLE"):
		return time.Duration(5*attempt) * time.Second, true
	}
	return 0, false
}

// generate calls Gemini, retrying rate-limit and overload errors.
func (c *Client) generate(ctx context.Context, history []*genai.Content) (*genai.GenerateContentResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := c.client.Models.GenerateContent(ctx, c.modelName, history, c.config)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		wait, ok := retryDelay(err, attempt)
		if !ok || attempt == maxAttempts {
			break
		}
		log.Printf("[Gemini] transient error (attempt %d/%d), retrying in %s", attempt, maxAttempts, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}
