package toolbox

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// kubectl is started as a child process. It must not inherit our Gemini key, but must keep
// the variables it needs (for example KUBECONFIG).
func TestKubectlChildDoesNotInheritGeminiVariables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a fake kubectl")
	}
	dir := t.TempDir()
	fake := "#!/bin/sh\necho \"key=[${GEMINI_API_KEY:-absent}] file=[${GEMINI_API_KEY_FILE:-absent}] llm=[${LLM_API_KEY:-absent}] oai=[${OPENAI_API_KEY:-absent}] ant=[${ANTHROPIC_API_KEY:-absent}] kubeconfig=[${KUBECONFIG:-absent}]\"\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GEMINI_API_KEY", "CANARY_KEY_SHOULD_NOT_REACH_KUBECTL")
	t.Setenv("GEMINI_API_KEY_FILE", "/var/run/secrets/gemini/key")
	t.Setenv("LLM_API_KEY", "CANARY_LLM_KEY")
	t.Setenv("OPENAI_API_KEY", "CANARY_OPENAI_KEY")
	t.Setenv("ANTHROPIC_API_KEY", "CANARY_ANTHROPIC_KEY")
	t.Setenv("KUBECONFIG", "/tmp/test-kubeconfig")

	ex := NewSandboxedExecutor(NewToolRegistry(), 10*time.Second, false)
	res, err := ex.Execute(context.Background(), "list_pods", map[string]string{"namespace": "default"})
	if err != nil {
		t.Fatalf("execute: %v (%+v)", err, res)
	}
	out := res.Stdout
	if strings.Contains(out, "CANARY") || !strings.Contains(out, "key=[absent]") || !strings.Contains(out, "file=[absent]") || !strings.Contains(out, "llm=[absent]") || !strings.Contains(out, "oai=[absent]") || !strings.Contains(out, "ant=[absent]") {
		t.Errorf("kubectl inherited a Gemini variable: %s", out)
	}
	if !strings.Contains(out, "kubeconfig=[/tmp/test-kubeconfig]") {
		t.Errorf("kubectl lost KUBECONFIG, which it needs: %s", out)
	}
}
