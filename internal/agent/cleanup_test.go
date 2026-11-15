package agent

import (
	"testing"

	"k8s-log-analyzer/internal/protocol"
)

func TestFinishDropsStateAndRunsHook(t *testing.T) {
	p := &ProducerAgent{
		investigations: map[string]*protocol.Investigation{"a": {}, "b": {}},
		followUpCounts: map[string]int{"a": 2, "b": 1},
	}
	var got []string
	p.SetCleanup(func(id string) { got = append(got, id) })
	p.finish("a")
	if _, ok := p.investigations["a"]; ok {
		t.Fatal("investigation a leaked")
	}
	if _, ok := p.followUpCounts["a"]; ok {
		t.Fatal("follow-up count a leaked")
	}
	if len(p.investigations) != 1 || len(got) != 1 || got[0] != "a" {
		t.Fatalf("b must stay and hook must run once for a: %v %v", p.investigations, got)
	}
}

func TestWithNamespace(t *testing.T) {
	in := []protocol.FetchRequestPayload{
		{ToolName: "get_pod_logs", Parameters: map[string]string{"pod": "x"}},
		{ToolName: "list_pods", Parameters: map[string]string{"namespace": "other"}},
		{ToolName: "get_events"},
	}
	out := withNamespace(in, "la-demo")
	if out[0].Parameters["namespace"] != "la-demo" || out[2].Parameters["namespace"] != "la-demo" {
		t.Fatalf("namespace not filled: %+v", out)
	}
	if out[1].Parameters["namespace"] != "other" {
		t.Fatal("existing namespace must be kept (policy decides if it is allowed)")
	}
	if _, ok := in[0].Parameters["namespace"]; ok {
		t.Fatal("input must not be mutated")
	}
}
