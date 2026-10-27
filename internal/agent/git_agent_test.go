package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s-log-analyzer/internal/protocol"
)

func TestAnalyzeRawLogs_OOM(t *testing.T) {
	logs := `
2026-10-27T05:00:01Z [info] Server starting up on port 8080
2026-10-27T05:05:10Z [warn] High memory pressure detected
2026-10-27T05:05:12Z java.lang.OutOfMemoryError: Java heap space
2026-10-27T05:05:13Z kernel: [12345.67] Out of memory: Kill process 99 (java) score 850 or sacrifice child
2026-10-27T05:05:14Z terminated with exit code 137
`
	issue := AnalyzeRawLogs(logs)
	if issue.Severity != "P0" {
		t.Fatalf("expected P0 severity for OOM, got %s", issue.Severity)
	}
	if !strings.Contains(issue.Title, "OOM") {
		t.Fatalf("expected title to mention OOM, got %s", issue.Title)
	}
	if !strings.Contains(issue.RootCause, "memory") {
		t.Fatalf("expected root cause to mention memory, got %s", issue.RootCause)
	}
}

func TestAnalyzeRawLogs_Panic(t *testing.T) {
	logs := `
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x123456]
goroutine 42 [running]:
main.processOrder(0x0)
	/app/order.go:88 +0x3a
`
	issue := AnalyzeRawLogs(logs)
	if issue.Severity != "P0" {
		t.Fatalf("expected P0 severity for Panic, got %s", issue.Severity)
	}
	if !strings.Contains(issue.Title, "Panic") {
		t.Fatalf("expected title to mention Panic, got %s", issue.Title)
	}
}

func TestAnalyzeRawLogs_Http5xxAndTimeouts(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString("2026-10-27 05:00:00 [error] upstream gateway timeout context deadline exceeded HTTP 504\n")
	}

	issue := AnalyzeRawLogs(sb.String())
	if issue.Severity != "P2" {
		t.Fatalf("expected P2 severity for timeouts, got %s", issue.Severity)
	}
}

func TestGitAgent_PersistIssueToRepo(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "git-agent-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Ensure git init works in temp dir
	initCmd := exec.Command("git", "init", "-b", "main")
	initCmd.Dir = tempDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %s (%v)", string(out), err)
	}

	// Create an initial commit so branch switching is valid
	dummyFile := filepath.Join(tempDir, "README.md")
	_ = os.WriteFile(dummyFile, []byte("# Test Repo\n"), 0644)
	_ = exec.Command("git", "-C", tempDir, "config", "user.name", "Test").Run()
	_ = exec.Command("git", "-C", tempDir, "config", "user.email", "test@test.local").Run()
	_ = exec.Command("git", "-C", tempDir, "add", "README.md").Run()
	_ = exec.Command("git", "-C", tempDir, "commit", "-m", "initial commit").Run()

	bus := protocol.NewAgentBus(10)
	cfg := DefaultGitAgentConfig()
	cfg.RepoDir = tempDir
	cfg.CreateBranch = true

	gitAgent := NewGitAgent(bus, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sampleLogs := `
2026-10-27T01:00:00Z [fatal] OutOfMemoryError: Java heap space
2026-10-27T01:00:01Z [error] container died with exit code 137
`

	req := protocol.GitPersistRequestPayload{
		InvestigationID: "test-inv-001",
		Title:           "Payment Service OOM Crash",
		Namespace:       "production",
		Service:         "payment-service",
		Symptom:         "Pods crashing with exit code 137",
		RawLogs:         sampleLogs,
		RepoDir:         tempDir,
		CreateBranch:    true,
	}

	res, err := gitAgent.PersistIssue(ctx, req)
	if err != nil {
		t.Fatalf("PersistIssue failed: %v", err)
	}

	if res.CommitHash == "" {
		t.Fatalf("expected commit hash to be non-empty")
	}

	if !strings.HasPrefix(res.FilePath, "incidents") {
		t.Fatalf("expected incident file path in incidents dir, got %s", res.FilePath)
	}

	// Verify file on disk exists and contains YAML frontmatter
	fullPath := filepath.Join(tempDir, res.FilePath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("failed to read persisted incident file: %v", err)
	}

	if !strings.Contains(string(content), "id: \"test-inv-001\"") {
		t.Fatalf("persisted file missing investigation id: %s", string(content))
	}

	if !strings.Contains(string(content), "severity: \"P0\"") {
		t.Fatalf("expected P0 severity in frontmatter: %s", string(content))
	}

	// Verify git log contains commit
	logCmd := exec.Command("git", "-C", tempDir, "log", "-n", "1", "--oneline")
	out, err := logCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !strings.Contains(string(out), "Payment Service OOM Crash") {
		t.Fatalf("git log does not contain commit message: %s", string(out))
	}
}
