package agent

import (
	"testing"

	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/toolbox"
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
	out := withNamespace(in, "la-demo", toolbox.NewToolRegistry())
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

// A node is not in a namespace: adding one made get_node_conditions fail its own parameter check
// on every call, so the model could never read node conditions.
func TestWithNamespaceSkipsToolsWithoutNamespace(t *testing.T) {
	in := []protocol.FetchRequestPayload{{ToolName: "get_node_conditions", Parameters: map[string]string{"node": "n1"}}}
	out := withNamespace(in, "la-demo", toolbox.NewToolRegistry())
	if _, ok := out[0].Parameters["namespace"]; ok {
		t.Fatalf("namespace must not be added to a tool that has no namespace parameter: %v", out[0].Parameters)
	}
}

// Every registered tool must get through the same steps a model's request takes in production:
// fill in the namespace, pass the policy, pass the tool's own parameter check. A tool that cannot is dead.
func TestEveryToolIsReachable(t *testing.T) {
	sample := map[string]string{
		"namespace": "la-demo", "pod": "web-1", "container": "app", "since": "1h", "tail_lines": "50",
		"previous": "false", "label_selector": "app=web", "field_selector": "type=Warning",
		"deployment": "web", "service": "web", "node": "node-1", "configmap": "cfg", "hpa": "web",
	}
	reg := toolbox.NewToolRegistry()
	pol := toolbox.NewPolicy(reg, toolbox.PolicyConfig{AllowSensitive: true})
	for _, tool := range reg.AllTools() {
		params := map[string]string{}
		for _, d := range tool.Parameters {
			v, ok := sample[d.Name]
			if !ok {
				t.Fatalf("tool %s has parameter %q with no sample value: add one to this test", tool.Name, d.Name)
			}
			if d.Name != "namespace" { // the producer fills the namespace in, as in production
				params[d.Name] = v
			}
		}
		req := withNamespace([]protocol.FetchRequestPayload{{ToolName: tool.Name, Parameters: params}}, "la-demo", reg)[0]
		if den, err := pol.Check("inv-"+tool.Name, toolbox.InvestigationScope{Namespace: "la-demo"}, req.ToolName, req.Parameters); err != nil {
			t.Errorf("%s: policy refused a valid call: %s", tool.Name, den)
		}
		if _, _, err := reg.ValidateAndBuild(req.ToolName, req.Parameters); err != nil {
			t.Errorf("%s: the tool refused a valid call: %v", tool.Name, err)
		}
	}
}
