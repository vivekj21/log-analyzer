package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s-log-analyzer/internal/toolbox"
)

// retryScale shrinks every wait in tests. Always time.Second in real use.
var retryScale = time.Second

const maxBodyBytes = 8 << 20

// httpCaller sends JSON to a model service and retries the failures worth retrying.
type httpCaller struct {
	tag     string // log prefix, never contains a secret
	client  *http.Client
	secrets []string // scrubbed from every error text
}

func newHTTPCaller(tag string, secrets ...string) *httpCaller {
	return &httpCaller{tag: tag, client: &http.Client{Timeout: 180 * time.Second}, secrets: secrets}
}

func (h *httpCaller) scrub(s string) string {
	for _, sec := range h.secrets {
		if len(sec) >= 8 {
			s = strings.ReplaceAll(s, sec, "[REDACTED]")
		}
	}
	return s
}

func excerpt(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// post sends body as JSON and returns the response body of a 2xx answer.
// Rate limits (429), overload (5xx, 529) and network hiccups are retried; everything else fails at once.
func (h *httpCaller) post(ctx context.Context, url string, headers map[string]string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := h.client.Do(req)
		var wait time.Duration
		var retry bool
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("request failed: %s", h.scrub(err.Error()))
			wait, retry = time.Duration(3*attempt)*retryScale, attempt < 3
		} else {
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
			resp.Body.Close()
			if rerr != nil {
				lastErr = fmt.Errorf("reading the reply failed: %s", h.scrub(rerr.Error()))
				wait, retry = time.Duration(3*attempt)*retryScale, attempt < 3
			} else if resp.StatusCode/100 == 2 {
				return data, nil
			} else {
				lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, h.scrub(excerpt(data)))
				wait, retry = retryAfterStatus(resp.StatusCode, resp.Header.Get("Retry-After"), attempt)
			}
		}
		if !retry || attempt == maxAttempts {
			break
		}
		log.Printf("[%s] transient error (attempt %d/%d), retrying in %s: %v", h.tag, attempt, maxAttempts, wait, lastErr)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// retryAfterStatus decides whether an HTTP status is worth retrying and how long to wait.
func retryAfterStatus(status int, retryAfter string, attempt int) (time.Duration, bool) {
	switch {
	case status == 429:
		if secs, err := strconv.ParseFloat(strings.TrimSpace(retryAfter), 64); err == nil {
			d := time.Duration(secs * float64(retryScale))
			if d > maxWait/time.Second*retryScale { // a long wait means a daily limit: do not sit and wait
				return 0, false
			}
			return d + retryScale, true
		}
		return time.Duration(10*attempt) * retryScale, attempt < 3
	case status == 500 || status == 502 || status == 503 || status == 504 || status == 529:
		return time.Duration(5*attempt) * retryScale, true
	}
	return 0, false
}

// guardedCall applies the budget around one model call.
func guardedCall(b *Budget, invID string, do func() (tokens int64, err error)) error {
	if b != nil {
		if err := b.Allow(invID); err != nil {
			return err
		}
	}
	tokens, err := do()
	if b != nil {
		switch {
		case err == nil:
			b.Succeed()
			b.Record(invID, tokens)
		default:
			b.Fail()
		}
	}
	return err
}

// ---- conversation state shared by the HTTP providers ----

type pendingCall struct{ id, name string }

type convo[M any] struct {
	msgs    []M
	pending []pendingCall // tool calls the model made that still need an answer
	touched time.Time
}

type convoStore[M any] struct {
	mu sync.Mutex
	m  map[string]*convo[M]
}

func newConvoStore[M any]() *convoStore[M] { return &convoStore[M]{m: map[string]*convo[M]{}} }

// with runs f on the conversation for id, creating it if needed and dropping idle ones.
func (s *convoStore[M]) with(id string, f func(c *convo[M])) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, c := range s.m {
		if now.Sub(c.touched) > historyTTL {
			delete(s.m, k)
		}
	}
	c := s.m[id]
	if c == nil {
		c = &convo[M]{}
		s.m[id] = c
	}
	c.touched = now
	f(c)
}

func (s *convoStore[M]) forget(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

// matchResults pairs tool results with the calls the model made, in order, by tool name.
// Calls without a result and results without a call are both returned so the caller can still
// build a valid message list.
func matchResults(pending []pendingCall, results []toolOutput) (paired []pairedResult, unmatched []toolOutput, unanswered []pendingCall) {
	used := make([]bool, len(pending))
	for _, r := range results {
		found := -1
		for i, p := range pending {
			if !used[i] && p.name == r.name {
				found = i
				break
			}
		}
		if found < 0 {
			unmatched = append(unmatched, r)
			continue
		}
		used[found] = true
		paired = append(paired, pairedResult{call: pending[found], out: r})
	}
	for i, p := range pending {
		if !used[i] {
			unanswered = append(unanswered, p)
		}
	}
	return
}

type toolOutput struct {
	name    string
	text    string
	isError bool
}

type pairedResult struct {
	call pendingCall
	out  toolOutput
}

func toolOutputText(name, result, errText string) toolOutput {
	if errText != "" {
		return toolOutput{name: name, text: fmt.Sprintf("ERROR: %s\nOutput: %s", errText, result), isError: true}
	}
	return toolOutput{name: name, text: result}
}

// toolSchema is a tool in the JSON-schema shape both providers use.
type toolSchema struct {
	Name        string
	Description string
	Parameters  map[string]any
}

func toolSchemas(tools []*toolbox.ToolDefinition) []toolSchema {
	out := make([]toolSchema, 0, len(tools))
	for _, t := range tools {
		props := map[string]any{}
		var required []string
		for _, p := range t.Parameters {
			prop := map[string]any{"type": "string", "description": p.Description}
			if len(p.AllowedValues) > 0 {
				prop["enum"] = p.AllowedValues
			}
			props[p.Name] = prop
			if p.Required {
				required = append(required, p.Name)
			}
		}
		params := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			params["required"] = required
		}
		out = append(out, toolSchema{Name: t.Name, Description: t.Description, Parameters: params})
	}
	return out
}

// argsToStrings turns decoded JSON arguments into the string map the rest of the program uses.
func argsToStrings(in map[string]any) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}
