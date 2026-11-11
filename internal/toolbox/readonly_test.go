package toolbox

import (
	"strings"
	"testing"
)

// Fails if any registered tool looks like it can change cluster state.
func TestNoWriteToolsRegistered(t *testing.T) {
	writeVerbs := []string{
		"delete", "apply", "patch", "exec", "edit", "scale",
		"create", "replace", "drain", "cordon",
	}
	tools := NewToolRegistry().AllTools()
	if len(tools) == 0 {
		t.Fatal("no tools registered; test would pass vacuously")
	}
	for _, tool := range tools {
		for _, part := range strings.Split(strings.ToLower(tool.Name), "_") {
			for _, verb := range writeVerbs {
				if part == verb {
					t.Errorf("tool %q contains write verb %q", tool.Name, verb)
				}
			}
		}
	}
}
