package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/toolbox"
)

const testKey = "sk-TESTKEY-0123456789abcdef"

func fastRetries(t *testing.T) {
	t.Helper()
	old := retryScale
	retryScale = time.Millisecond
	t.Cleanup(func() { retryScale = old })
}

// fakeServer records every request body and answers from a script.
type fakeServer struct {
	mu      sync.Mutex
	srv     *httptest.Server
	paths   []string
	headers []http.Header
	bodies  []map[string]any
}

func newFake(t *testing.T, respond func(n int, w http.ResponseWriter, r *http.Request)) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.headers = append(f.headers, r.Header.Clone())
		f.bodies = append(f.bodies, body)
		n := len(f.bodies)
		f.mu.Unlock()
		respond(n, w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.bodies) }

func openAIConfig(f *fakeServer) ProviderConfig {
	return ProviderConfig{Provider: ProviderOpenAI, APIKey: testKey, Model: "test-model", BaseURL: f.srv.URL}
}

func anthropicConfig(f *fakeServer) ProviderConfig {
	return ProviderConfig{Provider: ProviderAnthropic, APIKey: testKey, Model: "test-model", BaseURL: f.srv.URL}
}

func writeJSON(w http.ResponseWriter, v string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, v)
}

func messagesOf(body map[string]any) []map[string]any {
	var out []map[string]any
	for _, m := range body["messages"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

// ---- OpenAI-compatible ----

func TestOpenAI_FullToolLoop(t *testing.T) {
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"list_pods","arguments":"{\"namespace\":\"prod\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":100}}`)
			return
		}
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"ROOT_CAUSE_CATEGORY: OOM_KILLED"},"finish_reason":"stop"}],"usage":{"total_tokens":150}}`)
	})
	reg := toolbox.NewToolRegistry()
	c := newOpenAIClient(openAIConfig(f), reg)

	res, err := c.Analyze(context.Background(), "inv", "pods are restarting")
	if err != nil || !res.HasToolCalls || len(res.FunctionCalls) != 1 {
		t.Fatalf("step 1: %+v %v", res, err)
	}
	if fc := res.FunctionCalls[0]; fc.Name != "list_pods" || fc.Args["namespace"] != "prod" {
		t.Errorf("tool call parsed wrong: %+v", fc)
	}
	res, err = c.SendToolResults(context.Background(), "inv", []protocol.ToolCallResult{{ToolName: "list_pods", Result: "pod-a 0/1 OOMKilled"}})
	if err != nil || res.HasToolCalls || !strings.Contains(res.TextResponse, "OOM_KILLED") || res.TotalTokens != 150 {
		t.Fatalf("step 2: %+v %v", res, err)
	}

	if f.paths[0] != "/chat/completions" {
		t.Errorf("wrong path %q", f.paths[0])
	}
	if got := f.headers[0].Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("wrong auth header %q", got)
	}
	first := f.bodies[0]
	if first["model"] != "test-model" || len(first["tools"].([]any)) != len(reg.AllTools()) {
		t.Errorf("request body wrong: model=%v tools=%d (want %d)", first["model"], len(first["tools"].([]any)), len(reg.AllTools()))
	}
	m1 := messagesOf(first)
	if m1[0]["role"] != "system" || !strings.Contains(m1[0]["content"].(string), "SECURITY RULES") {
		t.Errorf("system prompt must carry the untrusted-data rules")
	}
	m2 := messagesOf(f.bodies[1])
	last := m2[len(m2)-1]
	if last["role"] != "tool" || last["tool_call_id"] != "call_1" || !strings.Contains(last["content"].(string), "OOMKilled") {
		t.Errorf("tool result not sent back correctly: %v", last)
	}
	if m2[len(m2)-2]["role"] != "assistant" {
		t.Errorf("the assistant's tool call must stay in the history: %v", m2[len(m2)-2])
	}
}

func TestOpenAI_EveryToolCallGetsAnAnswer(t *testing.T) {
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			writeJSON(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"list_pods","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"get_events","arguments":"{}"}}]}}],"usage":{"total_tokens":1}}`)
			return
		}
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}],"usage":{"total_tokens":1}}`)
	})
	c := newOpenAIClient(openAIConfig(f), toolbox.NewToolRegistry())
	if _, err := c.Analyze(context.Background(), "inv", "x"); err != nil {
		t.Fatal(err)
	}
	// Only one of the two calls is answered, plus one result for a call that was never made.
	if _, err := c.SendToolResults(context.Background(), "inv", []protocol.ToolCallResult{
		{ToolName: "get_events", Result: "ev"}, {ToolName: "describe_pod", Result: "stray"},
	}); err != nil {
		t.Fatal(err)
	}
	answers := map[string]string{}
	for _, m := range messagesOf(f.bodies[1]) {
		if m["role"] == "tool" {
			answers[m["tool_call_id"].(string)] = m["content"].(string)
		}
	}
	if answers["b"] != "ev" || !strings.Contains(answers["a"], "not executed") || len(answers) != 2 {
		t.Errorf("each tool call needs exactly one answer, got %v", answers)
	}
}

func TestOpenAI_RetriesRateLimitThenSucceeds(t *testing.T) {
	fastRetries(t)
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"total_tokens":1}}`)
	})
	c := newOpenAIClient(openAIConfig(f), toolbox.NewToolRegistry())
	res, err := c.Analyze(context.Background(), "inv", "x")
	if err != nil || res.TextResponse != "ok" || f.count() != 2 {
		t.Fatalf("want a retry then success: %+v %v calls=%d", res, err, f.count())
	}
}

func TestOpenAI_DailyLimitIsNotRetried(t *testing.T) {
	fastRetries(t)
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "86400")
		w.WriteHeader(429)
	})
	c := newOpenAIClient(openAIConfig(f), toolbox.NewToolRegistry())
	if _, err := c.Analyze(context.Background(), "inv", "x"); err == nil || f.count() != 1 {
		t.Fatalf("a long Retry-After means a daily limit: fail at once. err=%v calls=%d", err, f.count())
	}
}

func TestOpenAI_ClientErrorsAreNotRetriedAndNeverLeakTheKey(t *testing.T) {
	fastRetries(t)
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		// A badly behaved server that echoes the credential back.
		writeJSON(w, `{"error":"Incorrect API key provided: `+testKey+`"}`)
	})
	c := newOpenAIClient(openAIConfig(f), toolbox.NewToolRegistry())
	_, err := c.Analyze(context.Background(), "inv", "x")
	if err == nil || f.count() != 1 {
		t.Fatalf("401 must fail once: err=%v calls=%d", err, f.count())
	}
	if strings.Contains(err.Error(), testKey) || !strings.Contains(err.Error(), "401") {
		t.Errorf("error must show the status but hide the key: %v", err)
	}
}

func TestOpenAI_EmptyAnswerIsAnError(t *testing.T) {
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"length"}],"usage":{"total_tokens":1}}`)
	})
	c := newOpenAIClient(openAIConfig(f), toolbox.NewToolRegistry())
	if _, err := c.Analyze(context.Background(), "inv", "x"); err == nil || !strings.Contains(err.Error(), "length") {
		t.Fatalf("an empty answer must not become an empty report: %v", err)
	}
}

func TestOpenAI_BudgetStopsRunawayCalls(t *testing.T) {
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"total_tokens":1}}`)
	})
	c := newOpenAIClient(openAIConfig(f), toolbox.NewToolRegistry())
	c.budget = NewBudget(BudgetConfig{MaxCalls: 1})
	if _, err := c.Analyze(context.Background(), "inv", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Analyze(context.Background(), "inv", "y"); err == nil || !strings.Contains(err.Error(), "budget") || f.count() != 1 {
		t.Fatalf("second call should be refused by the budget without reaching the server: %v calls=%d", err, f.count())
	}
}

// ---- Anthropic ----

func TestAnthropic_FullToolLoopWithParallelCalls(t *testing.T) {
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			writeJSON(w, `{"content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"tu_1","name":"list_pods","input":{"namespace":"prod"}},{"type":"tool_use","id":"tu_2","name":"get_events","input":{"namespace":"prod"}}],"stop_reason":"tool_use","usage":{"input_tokens":70,"output_tokens":30}}`)
			return
		}
		writeJSON(w, `{"content":[{"type":"text","text":"ROOT_CAUSE_CATEGORY: IMAGE_PULL_FAIL"}],"stop_reason":"end_turn","usage":{"input_tokens":120,"output_tokens":30}}`)
	})
	reg := toolbox.NewToolRegistry()
	c := newAnthropicClient(anthropicConfig(f), reg)

	res, err := c.Analyze(context.Background(), "inv", "pod stuck")
	if err != nil || !res.HasToolCalls || len(res.FunctionCalls) != 2 || res.TotalTokens != 100 {
		t.Fatalf("step 1: %+v %v", res, err)
	}
	res, err = c.SendToolResults(context.Background(), "inv", []protocol.ToolCallResult{
		{ToolName: "list_pods", Result: "pod-a ImagePullBackOff"},
		{ToolName: "get_events", Result: "", Error: "forbidden"},
	})
	if err != nil || res.HasToolCalls || !strings.Contains(res.TextResponse, "IMAGE_PULL_FAIL") || res.TotalTokens != 150 {
		t.Fatalf("step 2: %+v %v", res, err)
	}

	if f.paths[0] != "/messages" || f.headers[0].Get("x-api-key") != testKey || f.headers[0].Get("anthropic-version") != anthropicVersion {
		t.Errorf("wrong path or headers: %q %v", f.paths[0], f.headers[0])
	}
	first := f.bodies[0]
	if !strings.Contains(first["system"].(string), "SECURITY RULES") || first["max_tokens"].(float64) != anthropicMaxOutput {
		t.Errorf("system prompt or max_tokens wrong")
	}
	tool0 := first["tools"].([]any)[0].(map[string]any)
	if _, ok := tool0["input_schema"]; !ok || len(first["tools"].([]any)) != len(reg.AllTools()) {
		t.Errorf("tools must use input_schema and cover the whole registry: %v", tool0)
	}
	m2 := messagesOf(f.bodies[1])
	if len(m2) != 3 || m2[1]["role"] != "assistant" || m2[2]["role"] != "user" {
		t.Fatalf("history must be user, assistant, user: %v", m2)
	}
	blocks := m2[2]["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("both tool results must be in ONE user message: %v", blocks)
	}
	b0, b1 := blocks[0].(map[string]any), blocks[1].(map[string]any)
	if b0["tool_use_id"] != "tu_1" || b1["tool_use_id"] != "tu_2" || b1["is_error"] != true || b0["is_error"] != nil {
		t.Errorf("tool results not matched to their calls: %v", blocks)
	}
	if !strings.Contains(m2[1]["content"].([]any)[1].(map[string]any)["id"].(string), "tu_1") {
		t.Errorf("the assistant's tool_use blocks must be kept exactly: %v", m2[1])
	}
}

func TestAnthropic_ErrorsNeverLeakTheKey(t *testing.T) {
	fastRetries(t)
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		writeJSON(w, `{"type":"error","error":{"message":"bad request for key `+testKey+`"}}`)
	})
	c := newAnthropicClient(anthropicConfig(f), toolbox.NewToolRegistry())
	_, err := c.Analyze(context.Background(), "inv", "x")
	if err == nil || strings.Contains(err.Error(), testKey) || f.count() != 1 {
		t.Fatalf("want one failed call with the key hidden: %v calls=%d", err, f.count())
	}
}

func TestAnthropic_OverloadedIsRetried(t *testing.T) {
	fastRetries(t)
	f := newFake(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 1 {
			w.WriteHeader(529)
			return
		}
		writeJSON(w, `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	c := newAnthropicClient(anthropicConfig(f), toolbox.NewToolRegistry())
	if res, err := c.Analyze(context.Background(), "inv", "x"); err != nil || res.TextResponse != "ok" || f.count() != 2 {
		t.Fatalf("529 should be retried: %+v %v calls=%d", res, err, f.count())
	}
}

// ---- provider settings ----

func TestProviderValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  ProviderConfig
		want string // substring of the error, "" for no error
	}{
		{"gemini default", ProviderConfig{Provider: ProviderGemini, Model: "m"}, ""},
		{"gemini with base url", ProviderConfig{Provider: ProviderGemini, BaseURL: "https://x"}, "not used"},
		{"openai needs a model", ProviderConfig{Provider: ProviderOpenAI}, "needs --model"},
		{"anthropic needs a model", ProviderConfig{Provider: ProviderAnthropic}, "needs --model"},
		{"openai default address", ProviderConfig{Provider: ProviderOpenAI, Model: "m"}, ""},
		{"https is fine", ProviderConfig{Provider: ProviderOpenAI, Model: "m", BaseURL: "https://api.groq.com/openai/v1"}, ""},
		{"localhost over http is fine", ProviderConfig{Provider: ProviderOpenAI, Model: "m", BaseURL: "http://localhost:11434/v1"}, ""},
		{"loopback ip over http is fine", ProviderConfig{Provider: ProviderOpenAI, Model: "m", BaseURL: "http://127.0.0.1:8000/v1"}, ""},
		{"plain http to another machine is refused", ProviderConfig{Provider: ProviderOpenAI, Model: "m", BaseURL: "http://10.0.0.5:8000/v1"}, "plain http"},
		{"plain http to a hostname is refused", ProviderConfig{Provider: ProviderOpenAI, Model: "m", BaseURL: "http://llm.example.com/v1"}, "plain http"},
		{"plain http is allowed when asked for", ProviderConfig{Provider: ProviderOpenAI, Model: "m", BaseURL: "http://ollama.models.svc:11434/v1", AllowHTTP: true}, ""},
		{"other schemes are refused", ProviderConfig{Provider: ProviderOpenAI, Model: "m", BaseURL: "ftp://x/v1"}, "https://"},
		{"unknown provider", ProviderConfig{Provider: "bard"}, "unknown --provider"},
	}
	for _, tc := range cases {
		err := tc.cfg.Validate()
		if tc.want == "" && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}

func TestNewLiveBuildsEachProvider(t *testing.T) {
	reg := toolbox.NewToolRegistry()
	for _, p := range []string{ProviderOpenAI, ProviderAnthropic} {
		m, err := NewLive(context.Background(), ProviderConfig{Provider: p, Model: "m", APIKey: testKey}, reg)
		if err != nil || m == nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		m.Forget("x")
		m.Close()
	}
	if _, err := NewLive(context.Background(), ProviderConfig{Provider: ProviderOpenAI, APIKey: testKey}, reg); err == nil {
		t.Errorf("a missing model must be refused before any call is made")
	}
}
