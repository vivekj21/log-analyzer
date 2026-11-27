package llm

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"k8s-log-analyzer/internal/protocol"
)

// podNameRegex extracts the first pod name from kubectl output.
var podNameRegex = regexp.MustCompile(`([a-zA-Z0-9][-a-zA-Z0-9]+-[a-z0-9]+-[a-z0-9]+)`)

// HeuristicModel is a rule-based, non-AI implementation of Model using strings.Contains.
// It remembers, per investigation, what the pod table of the first prompt showed, so that the
// later tool output (which has no pod table) is judged against the same facts.
type HeuristicModel struct {
	mu     sync.Mutex
	tables map[string]podTableState
}

func NewHeuristicModel() *HeuristicModel {
	return &HeuristicModel{tables: map[string]podTableState{}}
}

func (h *HeuristicModel) Analyze(ctx context.Context, invID, prompt string) (*AnalysisResult, error) {
	tbl := tableState(prompt)
	if cat, expl, ev, ok := matchRules(prompt, tbl); ok {
		return &AnalysisResult{TextResponse: formatHeuristicReport(cat, expl, ev)}, nil
	}

	// Look into a suspicious pod. A pod is suspicious if the pod table shows it is not Running,
	// not fully ready, or has restarted (this does not depend on lucky wording), or if the text
	// mentions a failure word. Logs alone often miss the cause (an out-of-memory kill is recorded
	// in the pod description, not in its logs), so read both.
	lower := strings.ToLower(prompt)
	pod, suspicious := suspectPod(prompt)
	if pod != "" && (suspicious || hasFailureWord(lower)) {
		h.mu.Lock()
		if h.tables == nil {
			h.tables = map[string]podTableState{}
		}
		h.tables[invID] = tbl
		h.mu.Unlock()
		return &AnalysisResult{
			HasToolCalls: true,
			FunctionCalls: []FunctionCallRequest{
				{Name: "describe_pod", Args: map[string]string{"pod": pod}},
				{Name: "get_pod_logs", Args: map[string]string{"pod": pod, "tail_lines": "100"}},
			},
		}, nil
	}

	return &AnalysisResult{TextResponse: formatCannotDetermine("The rule-based model found no pod list or recognizable error text to work from.")}, nil
}

func (h *HeuristicModel) SendToolResults(ctx context.Context, invID string, r []protocol.ToolCallResult) (*AnalysisResult, error) {
	var combined strings.Builder
	for _, res := range r {
		combined.WriteString(res.Result)
		combined.WriteString("\n")
		combined.WriteString(res.Error)
		combined.WriteString("\n")
	}

	h.mu.Lock()
	tbl, known := h.tables[invID]
	delete(h.tables, invID)
	h.mu.Unlock()
	if !known {
		tbl = tableState(combined.String())
	}
	if cat, expl, ev, ok := matchRules(combined.String(), tbl); ok {
		return &AnalysisResult{TextResponse: formatHeuristicReport(cat, expl, ev)}, nil
	}

	return &AnalysisResult{TextResponse: formatCannotDetermine("The rule-based model read the data but matched none of its known error patterns.")}, nil
}

// podTableState says what the pod table (kubectl get pods) in a text shows.
// seen: there is a table. pullWait: a pod row currently shows an image-pull wait status.
type podTableState struct{ seen, pullWait bool }

// tableState reads the pod table in text, if there is one.
func tableState(text string) podTableState {
	var st podTableState
	for _, m := range podRowRegex.FindAllStringSubmatch(text, -1) {
		st.seen = true
		status := strings.ToLower(m[4])
		// "ErrImagePull", "ImagePullBackOff", "Init:ImagePullBackOff", "InvalidImageName"
		if strings.Contains(status, "imagepull") || strings.Contains(status, "invalidimagename") {
			st.pullWait = true
		}
	}
	return st
}

type rule struct {
	category, explanation string
	any                   []string // one of these must be in the text ...
	also                  []string // ... and, if set, one of these too
}

var rules = []rule{
	{CategoryOOMKilled, "Container exceeded memory limit and was killed (OOMKilled).", []string{"oomkilled", "outofmemory", "exit code 137", "memory limit exceeded"}, nil},
	{CategoryImagePullFail, "Failed to pull container image from registry.", []string{"imagepullbackoff", "errimagepull", "manifest unknown", "failed to pull image"}, nil},
	{CategoryDBLock, "Database lock contention or deadlock detected.", []string{"deadlock", "lock wait timeout", "database lock", "lock contention"}, nil},
	{CategoryReadinessProbeFail, "Readiness probe failed; container not marked ready.", []string{"readiness probe failed", "probe failed", "unhealthy"}, nil},
	{CategoryCrashLoopBadConfig, "Application crashed due to bad configuration or unhandled panic.", []string{"panic:", "missing required configuration", "configuration validation failed", "syntax error", "invalid configuration", "required setting"}, nil},
	{CategoryNoFault, "No fault detected; services operating normally.", []string{"no events found"}, []string{"running", "200 ok"}},
}

// evaluateRules matches text on its own (the pod table, if any, is the one inside text).
func evaluateRules(text string) (string, string, bool) {
	cat, expl, _, ok := matchRules(text, tableState(text))
	return cat, expl, ok
}

// matchRules returns the first rule that matches, and the line of text that matched (trimmed).
// An old "Failed to pull image" event stays in the namespace events after the pod recovered.
// So when a pod table is known, the image-pull rule fires only if a pod is waiting on an image
// right now. With no table (a plain log file) the words alone decide, as before.
func matchRules(text string, tbl podTableState) (category, explanation, evidence string, ok bool) {
	lower := strings.ToLower(text)
	for _, r := range rules {
		if r.category == CategoryImagePullFail && tbl.seen && !tbl.pullWait {
			continue
		}
		kw := firstContained(lower, r.any)
		if kw == "" || (len(r.also) > 0 && firstContained(lower, r.also) == "") {
			continue
		}
		return r.category, r.explanation, lineWith(text, kw), true
	}
	return "", "", "", false
}

func firstContained(lower string, words []string) string {
	for _, w := range words {
		if strings.Contains(lower, w) {
			return w
		}
	}
	return ""
}

// lineWith returns the first line of text that contains word (any case), trimmed and cut to 200 characters.
func lineWith(text, word string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(strings.ToLower(line), word) {
			line = strings.Join(strings.Fields(line), " ")
			if r := []rune(line); len(r) > 200 {
				line = string(r[:200]) + "..."
			}
			return line
		}
	}
	return ""
}

func formatHeuristicReport(category, explanation, evidence string) string {
	ev := "Matched heuristic pattern"
	if evidence != "" {
		ev += ": `" + strings.ReplaceAll(evidence, "`", "'") + "`"
	}
	return fmt.Sprintf("Root Cause: %s\nConfidence: HIGH\nEvidence:\n- %s\n\nRecommendations:\n1. Apply standard remediation for %s.\n\nROOT_CAUSE_CATEGORY: %s\n",
		explanation, ev, category, category)
}

// formatCannotDetermine is the honest answer when the rules have nothing to go on.
// It must not look like a diagnosis, and it must not say the workload is healthy.
func formatCannotDetermine(why string) string {
	return fmt.Sprintf("Root Cause: Cannot tell. %s\nConfidence: LOW\nEvidence:\n- No rule matched the data that was collected\n\nRecommendations:\n1. Check the pods and their logs by hand, or re-run with --llm live for a deeper look.\n\nROOT_CAUSE_CATEGORY: %s\n", why, CategoryCannotDetermine)
}

// failureWords are signs in free text that something is wrong.
var failureWords = []string{"crash", "error", "fail", "timeout", "latency", "slow", "5xx", "504", "backoff", "killed", "evict"}

func hasFailureWord(lower string) bool {
	for _, w := range failureWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// podRowRegex matches a row of a "kubectl get pods" table: NAME READY STATUS RESTARTS.
// RESTARTS may look like "3" or "3 (45s ago)"; only the number is read.
var podRowRegex = regexp.MustCompile(`(?m)^\s*([a-z0-9][-a-z0-9.]*)\s+(\d+)/(\d+)\s+(\S+)\s+(\d+)`)

// suspectPod returns the pod most worth a closer look and whether the table itself says it is
// unhealthy: not Running/Completed, not fully ready, or restarted at least once.
// With no unhealthy pod it returns the first pod (suspicious=false); with no table, "".
func suspectPod(text string) (pod string, suspicious bool) {
	first := ""
	best, bestRestarts := "", 0
	for _, m := range podRowRegex.FindAllStringSubmatch(text, -1) {
		if first == "" {
			first = m[1]
		}
		restarts, _ := strconv.Atoi(m[5])
		bad := (m[4] != "Running" && m[4] != "Completed") || (m[4] == "Running" && m[2] != m[3])
		if bad {
			return m[1], true
		}
		if restarts > bestRestarts {
			best, bestRestarts = m[1], restarts
		}
	}
	if best != "" {
		return best, true
	}
	return first, false
}

// pickPod returns the pod suspectPod chooses ("" if there is no pod table).
func pickPod(text string) string {
	pod, _ := suspectPod(text)
	return pod
}
