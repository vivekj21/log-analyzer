// Package toolbox — policy.go enforces runtime guardrails on model-requested
// tool calls.  It is the second line of defence after the ToolRegistry's
// allowlist/parameter validation: it checks investigation-scoped constraints
// (namespace confinement, call budget, duplicate suppression, and sensitivity
// classification) before any command is actually executed.
package toolbox

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Risk classification
// ---------------------------------------------------------------------------

// RiskLevel classifies a tool's security sensitivity.
type RiskLevel int

const (
	// ReadOnly tools only read cluster state (the default).
	ReadOnly RiskLevel = iota
	// Sensitive tools can expose configuration data that may contain
	// credentials, tokens, or other secret material.
	Sensitive
)

func (r RiskLevel) String() string {
	switch r {
	case ReadOnly:
		return "ReadOnly"
	case Sensitive:
		return "Sensitive"
	default:
		return fmt.Sprintf("RiskLevel(%d)", int(r))
	}
}

// ---------------------------------------------------------------------------
// Policy configuration
// ---------------------------------------------------------------------------

// PolicyConfig carries operator-supplied knobs that relax or tighten the
// default policy.  Zero-value gives the most restrictive defaults.
type PolicyConfig struct {
	// AllowSensitive permits tools marked Sensitive.  Default false.
	AllowSensitive bool
	// MaxCallsPerInvestigation caps the total tool invocations per
	// investigation.  Zero means use the default (12).
	MaxCallsPerInvestigation int
}

const defaultMaxCalls = 12

func (c PolicyConfig) maxCalls() int {
	if c.MaxCallsPerInvestigation > 0 {
		return c.MaxCallsPerInvestigation
	}
	return defaultMaxCalls
}

// ---------------------------------------------------------------------------
// Investigation scope — passed in by the caller
// ---------------------------------------------------------------------------

// InvestigationScope carries the context that policy rules need.
type InvestigationScope struct {
	Namespace string // the namespace this investigation is confined to
}

// ---------------------------------------------------------------------------
// Policy engine
// ---------------------------------------------------------------------------

// Policy enforces per-investigation guardrails on tool calls requested by the
// model.  It is safe for concurrent use.
type Policy struct {
	config   PolicyConfig
	registry *ToolRegistry

	mu          sync.Mutex
	callCounts  map[string]int            // investigationID → count
	callHistory map[string]map[string]int // investigationID → callKey → count
}

// NewPolicy creates a Policy bound to the given registry and config.
func NewPolicy(registry *ToolRegistry, config PolicyConfig) *Policy {
	return &Policy{
		config:      config,
		registry:    registry,
		callCounts:  make(map[string]int),
		callHistory: make(map[string]map[string]int),
	}
}

// Check validates a tool call against the policy rules.  On success it
// returns ("", nil).  On rejection it returns a human-readable denial string
// suitable for sending back to the model as the tool result, and a non-nil
// error.
//
// The caller MUST pass the result string back to the model so it can adapt
// its plan, and log the denial.
func (p *Policy) Check(investigationID string, scope InvestigationScope, toolName string, params map[string]string) (denial string, err error) {
	// ------------------------------------------------------------------
	// Rule 0: reject shell metacharacters and flag-like values
	// ------------------------------------------------------------------
	if reason := rejectDangerousParams(params); reason != "" {
		return p.deny(investigationID, toolName, reason), fmt.Errorf("policy: %s", reason)
	}

	// ------------------------------------------------------------------
	// Rule 1: namespace confinement
	// ------------------------------------------------------------------
	if ns, ok := params["namespace"]; ok {
		if ns != scope.Namespace {
			reason := fmt.Sprintf("namespace %q is outside the investigation scope %q", ns, scope.Namespace)
			return p.deny(investigationID, toolName, reason), fmt.Errorf("policy: %s", reason)
		}
	}

	// ------------------------------------------------------------------
	// Rule 2: sensitivity gate
	// ------------------------------------------------------------------
	tool := p.registry.Lookup(toolName)
	if tool != nil && tool.Risk == Sensitive && !p.config.AllowSensitive {
		reason := fmt.Sprintf("tool %q is classified Sensitive and AllowSensitive is false", toolName)
		return p.deny(investigationID, toolName, reason), fmt.Errorf("policy: %s", reason)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// ------------------------------------------------------------------
	// Rule 3: per-investigation call budget
	// ------------------------------------------------------------------
	count := p.callCounts[investigationID]
	if count >= p.config.maxCalls() {
		reason := fmt.Sprintf("investigation exceeded the maximum of %d tool calls", p.config.maxCalls())
		return p.denyLocked(investigationID, toolName, reason), fmt.Errorf("policy: %s", reason)
	}

	// ------------------------------------------------------------------
	// Rule 4: duplicate call suppression
	// ------------------------------------------------------------------
	key := callKey(toolName, params)
	hist := p.callHistory[investigationID]
	if hist == nil {
		hist = make(map[string]int)
		p.callHistory[investigationID] = hist
	}
	if hist[key] > 0 {
		reason := fmt.Sprintf("identical call to %q with the same parameters was already executed", toolName)
		return p.denyLocked(investigationID, toolName, reason), fmt.Errorf("policy: %s", reason)
	}

	// All checks passed — record the call.
	p.callCounts[investigationID] = count + 1
	hist[key] = 1

	return "", nil
}

// ResetInvestigation clears counters for a finished investigation so the
// maps don't grow unboundedly.
func (p *Policy) ResetInvestigation(investigationID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.callCounts, investigationID)
	delete(p.callHistory, investigationID)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// rejectDangerousParams checks every parameter value for shell
// metacharacters and values that look like CLI flags (start with "-").
func rejectDangerousParams(params map[string]string) string {
	for name, val := range params {
		if containsShellMetachars(val) {
			return fmt.Sprintf("parameter %q contains forbidden shell metacharacters", name)
		}
		if strings.HasPrefix(val, "-") {
			return fmt.Sprintf("parameter %q value must not start with '-' (potential flag injection)", name)
		}
	}
	return ""
}

// callKey produces a deterministic string from a tool call so duplicates can
// be detected.  Parameter pairs are sorted for stability.
func callKey(toolName string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(toolName)
	for _, k := range keys {
		sb.WriteByte('\x00')
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(params[k])
	}
	return sb.String()
}

func (p *Policy) deny(investigationID, toolName, reason string) string {
	msg := fmt.Sprintf("DENIED: %s", reason)
	log.Printf("[Policy] investigation=%s tool=%s %s", investigationID, toolName, msg)
	return msg
}

// denyLocked is the same as deny but called when p.mu is already held.
func (p *Policy) denyLocked(investigationID, toolName, reason string) string {
	return p.deny(investigationID, toolName, reason)
}
