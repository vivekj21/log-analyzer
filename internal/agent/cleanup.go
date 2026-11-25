package agent

import (
	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/toolbox"
)

// SetCleanup registers a hook called once when an investigation ends, so other
// agents and the policy can drop their per-investigation state.
func (p *ProducerAgent) SetCleanup(fn func(id string)) {
	p.mu.Lock()
	p.onFinish = fn
	p.mu.Unlock()
}

// finish drops the producer's own state for an investigation and runs the hook.
func (p *ProducerAgent) finish(id string) {
	p.mu.Lock()
	delete(p.investigations, id)
	delete(p.followUpCounts, id)
	fn := p.onFinish
	p.mu.Unlock()
	if fn != nil {
		fn(id)
	}
}

// withNamespace returns copies of the requests with "namespace" filled in
// (from the investigation's own namespace) when it is missing, but only for tools that
// have a namespace parameter: a node is not in a namespace, and adding one makes the
// tool's own parameter check reject the call.
func withNamespace(reqs []protocol.FetchRequestPayload, ns string, reg *toolbox.ToolRegistry) []protocol.FetchRequestPayload {
	out := make([]protocol.FetchRequestPayload, len(reqs))
	for i, r := range reqs {
		params := make(map[string]string, len(r.Parameters)+1)
		for k, v := range r.Parameters {
			params[k] = v
		}
		if params["namespace"] == "" && ns != "" && takesNamespace(reg, r.ToolName) {
			params["namespace"] = ns
		}
		r.Parameters = params
		out[i] = r
	}
	return out
}

// takesNamespace says whether the named tool declares a "namespace" parameter.
func takesNamespace(reg *toolbox.ToolRegistry, tool string) bool {
	if reg == nil {
		return true
	}
	t := reg.Lookup(tool)
	if t == nil {
		return false
	}
	for _, p := range t.Parameters {
		if p.Name == "namespace" {
			return true
		}
	}
	return false
}
