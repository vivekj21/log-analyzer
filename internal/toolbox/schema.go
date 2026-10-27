// Package toolbox implements the hardcoded Tool Schema / Allowlist that enforces
// a "default-deny" security boundary for the Log Fetcher Agent.
package toolbox

import (
	"fmt"
	"regexp"
	"strings"
)

type ParameterType int

const (
	ParamString ParameterType = iota
	ParamInt
	ParamBool
)

type ParameterDef struct {
	Name          string
	Type          ParameterType
	Description   string
	Required      bool
	Default       string
	Pattern       *regexp.Regexp
	AllowedValues []string
	MaxLength     int
}

type ToolDefinition struct {
	Name           string
	Description    string
	Parameters     []ParameterDef
	CommandBuilder func(params map[string]string) (binary string, args []string, err error)
}

var (
	k8sNamePattern       = regexp.MustCompile(`^[a-z0-9]([a-z0-9.\-]{0,251}[a-z0-9])?$`)
	k8sNamespacePattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)
	containerNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)
	durationPattern      = regexp.MustCompile(`^([0-9]+[smh])+$`)
	selectorPattern      = regexp.MustCompile(`^[a-zA-Z0-9._\-/]+(=[a-zA-Z0-9._\-]+)?(,[a-zA-Z0-9._\-/]+(=[a-zA-Z0-9._\-]+)?)*$`)
	tailLinesPattern     = regexp.MustCompile(`^[0-9]{1,5}$`)
)

var dangerousSubstrings = []string{
	";", "|", "&", "`",
	"$(", "${",
	">", "<",
	"\n", "\r",
	"\\",
}

func containsShellMetachars(s string) bool {
	for _, d := range dangerousSubstrings {
		if strings.Contains(s, d) {
			return true
		}
	}
	return false
}

type ToolRegistry struct {
	tools map[string]*ToolDefinition
}

func NewToolRegistry() *ToolRegistry {
	r := &ToolRegistry{tools: make(map[string]*ToolDefinition)}
	r.registerAllTools()
	return r
}

func (r *ToolRegistry) Lookup(name string) *ToolDefinition {
	return r.tools[name]
}

func (r *ToolRegistry) AllTools() []*ToolDefinition {
	out := make([]*ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

func (r *ToolRegistry) ValidateAndBuild(toolName string, params map[string]string) (string, []string, error) {
	tool := r.Lookup(toolName)
	if tool == nil {
		return "", nil, fmt.Errorf("DENIED: tool %q is not in the allowlist", toolName)
	}

	if err := validateParams(tool, params); err != nil {
		return "", nil, fmt.Errorf("DENIED: parameter validation failed for tool %q: %w", toolName, err)
	}

	return tool.CommandBuilder(params)
}

func validateParams(tool *ToolDefinition, params map[string]string) error {
	known := make(map[string]bool, len(tool.Parameters))
	for _, p := range tool.Parameters {
		known[p.Name] = true
	}
	for k := range params {
		if !known[k] {
			return fmt.Errorf("unknown parameter %q — not declared in tool schema", k)
		}
	}

	for _, def := range tool.Parameters {
		val, exists := params[def.Name]
		if !exists || val == "" {
			if def.Required {
				return fmt.Errorf("required parameter %q is missing", def.Name)
			}
			continue
		}

		if def.MaxLength > 0 && len(val) > def.MaxLength {
			return fmt.Errorf("parameter %q exceeds max length %d (got %d)", def.Name, def.MaxLength, len(val))
		}

		if len(def.AllowedValues) > 0 {
			found := false
			for _, av := range def.AllowedValues {
				if val == av {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("parameter %q value %q not in allowed set %v", def.Name, val, def.AllowedValues)
			}
		}

		if def.Pattern != nil && !def.Pattern.MatchString(val) {
			return fmt.Errorf("parameter %q value %q does not match required pattern %s", def.Name, val, def.Pattern.String())
		}

		if containsShellMetachars(val) {
			return fmt.Errorf("parameter %q contains forbidden shell metacharacters", def.Name)
		}
	}

	return nil
}

func (r *ToolRegistry) registerAllTools() {
	r.tools["get_pod_logs"] = &ToolDefinition{
		Name:        "get_pod_logs",
		Description: "Fetch stdout/stderr logs from a specific pod.",
		Parameters: []ParameterDef{
			{Name: "namespace", Type: ParamString, Description: "Kubernetes namespace", Required: true, Pattern: k8sNamespacePattern, MaxLength: 63},
			{Name: "pod", Type: ParamString, Description: "Pod name", Required: true, Pattern: k8sNamePattern, MaxLength: 253},
			{Name: "container", Type: ParamString, Description: "Container name", Required: false, Pattern: containerNamePattern, MaxLength: 63},
			{Name: "since", Type: ParamString, Description: "Duration newer than (e.g. 1h, 30m)", Required: false, Pattern: durationPattern, MaxLength: 10},
			{Name: "tail_lines", Type: ParamString, Description: "Lines to tail", Required: false, Pattern: tailLinesPattern, MaxLength: 5},
			{Name: "previous", Type: ParamString, Description: "Previous container instance logs", Required: false, AllowedValues: []string{"true", "false"}, MaxLength: 5},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			args := []string{"logs", params["pod"], "-n", params["namespace"]}
			if v := params["container"]; v != "" {
				args = append(args, "-c", v)
			}
			if v := params["since"]; v != "" {
				args = append(args, "--since="+v)
			}
			if v := params["tail_lines"]; v != "" {
				args = append(args, "--tail="+v)
			}
			if params["previous"] == "true" {
				args = append(args, "--previous")
			}
			return "kubectl", args, nil
		},
	}

	r.tools["list_pods"] = &ToolDefinition{
		Name:        "list_pods",
		Description: "List all pods in a namespace showing status, restarts, and nodes.",
		Parameters: []ParameterDef{
			{Name: "namespace", Type: ParamString, Description: "Kubernetes namespace", Required: true, Pattern: k8sNamespacePattern, MaxLength: 63},
			{Name: "label_selector", Type: ParamString, Description: "Label selector (e.g. app=api)", Required: false, Pattern: selectorPattern, MaxLength: 256},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			args := []string{"get", "pods", "-n", params["namespace"], "-o", "wide"}
			if v := params["label_selector"]; v != "" {
				args = append(args, "-l", v)
			}
			return "kubectl", args, nil
		},
	}

	r.tools["describe_pod"] = &ToolDefinition{
		Name:        "describe_pod",
		Description: "Get the full description of a pod including events, limits, and container states.",
		Parameters: []ParameterDef{
			{Name: "namespace", Type: ParamString, Description: "Kubernetes namespace", Required: true, Pattern: k8sNamespacePattern, MaxLength: 63},
			{Name: "pod", Type: ParamString, Description: "Pod name", Required: true, Pattern: k8sNamePattern, MaxLength: 253},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			return "kubectl", []string{"describe", "pod", params["pod"], "-n", params["namespace"]}, nil
		},
	}

	r.tools["get_events"] = &ToolDefinition{
		Name:        "get_events",
		Description: "Get Kubernetes events in a namespace sorted by time.",
		Parameters: []ParameterDef{
			{Name: "namespace", Type: ParamString, Description: "Kubernetes namespace", Required: true, Pattern: k8sNamespacePattern, MaxLength: 63},
			{Name: "field_selector", Type: ParamString, Description: "Field selector", Required: false, Pattern: selectorPattern, MaxLength: 256},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			args := []string{"get", "events", "-n", params["namespace"], "--sort-by=.lastTimestamp"}
			if v := params["field_selector"]; v != "" {
				args = append(args, "--field-selector="+v)
			}
			return "kubectl", args, nil
		},
	}

	r.tools["get_deployment"] = &ToolDefinition{
		Name:        "get_deployment",
		Description: "Get a Deployment spec including replicas and rollout conditions.",
		Parameters: []ParameterDef{
			{Name: "namespace", Type: ParamString, Description: "Kubernetes namespace", Required: true, Pattern: k8sNamespacePattern, MaxLength: 63},
			{Name: "deployment", Type: ParamString, Description: "Deployment name", Required: true, Pattern: k8sNamePattern, MaxLength: 253},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			return "kubectl", []string{"get", "deployment", params["deployment"], "-n", params["namespace"], "-o", "yaml"}, nil
		},
	}

	r.tools["get_service_endpoints"] = &ToolDefinition{
		Name:        "get_service_endpoints",
		Description: "Describe a Service and its endpoints.",
		Parameters: []ParameterDef{
			{Name: "namespace", Type: ParamString, Description: "Kubernetes namespace", Required: true, Pattern: k8sNamespacePattern, MaxLength: 63},
			{Name: "service", Type: ParamString, Description: "Service name", Required: true, Pattern: k8sNamePattern, MaxLength: 253},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			return "kubectl", []string{"describe", "service", params["service"], "-n", params["namespace"]}, nil
		},
	}

	r.tools["get_node_conditions"] = &ToolDefinition{
		Name:        "get_node_conditions",
		Description: "Describe a node to check resource pressure conditions and taints.",
		Parameters: []ParameterDef{
			{Name: "node", Type: ParamString, Description: "Node name", Required: true, Pattern: k8sNamePattern, MaxLength: 253},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			return "kubectl", []string{"describe", "node", params["node"]}, nil
		},
	}

	r.tools["get_resource_usage"] = &ToolDefinition{
		Name:        "get_resource_usage",
		Description: "Get CPU/memory usage metrics for pods.",
		Parameters: []ParameterDef{
			{Name: "namespace", Type: ParamString, Description: "Kubernetes namespace", Required: true, Pattern: k8sNamespacePattern, MaxLength: 63},
			{Name: "pod", Type: ParamString, Description: "Pod name (optional)", Required: false, Pattern: k8sNamePattern, MaxLength: 253},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			if v := params["pod"]; v != "" {
				return "kubectl", []string{"top", "pod", v, "-n", params["namespace"]}, nil
			}
			return "kubectl", []string{"top", "pod", "-n", params["namespace"]}, nil
		},
	}

	r.tools["get_configmap"] = &ToolDefinition{
		Name:        "get_configmap",
		Description: "Retrieve a ConfigMap data specification.",
		Parameters: []ParameterDef{
			{Name: "namespace", Type: ParamString, Description: "Kubernetes namespace", Required: true, Pattern: k8sNamespacePattern, MaxLength: 63},
			{Name: "configmap", Type: ParamString, Description: "ConfigMap name", Required: true, Pattern: k8sNamePattern, MaxLength: 253},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			return "kubectl", []string{"get", "configmap", params["configmap"], "-n", params["namespace"], "-o", "yaml"}, nil
		},
	}

	r.tools["get_hpa_status"] = &ToolDefinition{
		Name:        "get_hpa_status",
		Description: "Get HorizontalPodAutoscaler scaling metrics and status.",
		Parameters: []ParameterDef{
			{Name: "namespace", Type: ParamString, Description: "Kubernetes namespace", Required: true, Pattern: k8sNamespacePattern, MaxLength: 63},
			{Name: "hpa", Type: ParamString, Description: "HPA name (optional)", Required: false, Pattern: k8sNamePattern, MaxLength: 253},
		},
		CommandBuilder: func(params map[string]string) (string, []string, error) {
			if v := params["hpa"]; v != "" {
				return "kubectl", []string{"get", "hpa", v, "-n", params["namespace"], "-o", "yaml"}, nil
			}
			return "kubectl", []string{"get", "hpa", "-n", params["namespace"]}, nil
		},
	}
}
