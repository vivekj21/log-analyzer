package agent

import "k8s-log-analyzer/internal/llm"

// Forget releases the state kept for a finished investigation.
func (f *FetcherAgent) Forget(id string) { f.investigations.Delete(id) }

// Forget releases the token and any model state kept for a finished investigation.
func (a *AnalyzerAgent) Forget(id string) {
	a.tokens.Delete(id)
	if f, ok := a.model.(llm.Forgetter); ok {
		f.Forget(id)
	}
}
