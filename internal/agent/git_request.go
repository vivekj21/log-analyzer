package agent

import (
	"fmt"
	"strings"

	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/rootcause"
)

// SetCreateBranch controls whether live investigations commit to a new incident branch.
func (p *ProducerAgent) SetCreateBranch(b bool) {
	p.mu.Lock()
	p.createBranch = b
	p.mu.Unlock()
}

// buildGitRequest builds the commit request. When the AI produced an answer (raw),
// its category, summary and severity are used; log scanning is only the fallback.
func buildGitRequest(inv *protocol.Investigation, rootCause, raw, report, repoDir string, createBranch bool) protocol.GitPersistRequestPayload {
	var logs strings.Builder
	for _, ev := range inv.Evidence {
		if ev.Output != "" {
			logs.WriteString(fmt.Sprintf("--- [%s] ---\n%s\n\n", ev.ToolName, ev.Output))
		}
	}
	title := fmt.Sprintf("Incident in %s: %s", inv.Namespace, inv.Symptom)
	if len(title) > 60 {
		title = title[:60]
	}
	req := protocol.GitPersistRequestPayload{
		InvestigationID: inv.ID,
		Title:           title,
		Namespace:       inv.Namespace,
		Service:         inv.Service,
		Symptom:         inv.Symptom,
		RootCause:       rootCause,
		ReportMarkdown:  report,
		RawLogs:         logs.String(),
		RepoDir:         repoDir,
		CreateBranch:    createBranch,
	}
	if strings.TrimSpace(raw) != "" {
		cat := rootcause.Category(raw)
		if req.RootCause == "" {
			req.RootCause = rootcause.Summary(raw)
		}
		req.Severity = rootcause.Severity(cat)
		if cat != "" {
			req.ErrorSignatures = []string{cat}
		}
	}
	return req
}

// filterEventsForService keeps the header and only the event lines that mention the service.
func filterEventsForService(out, svc string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if svc == "" || len(lines) < 2 {
		return out
	}
	kept := []string{lines[0]}
	for _, l := range lines[1:] {
		if strings.Contains(l, svc) {
			kept = append(kept, l)
		}
	}
	if len(kept) == 1 {
		kept = append(kept, "No events found for service "+svc)
	}
	return strings.Join(kept, "\n") + "\n"
}
