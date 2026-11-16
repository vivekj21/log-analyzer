package toolbox

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	DefaultTimeout = 30 * time.Second
	MaxOutputBytes = 1 << 20 // 1 MiB
)

type ExecutionResult struct {
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration"`
	Error    string        `json:"error,omitempty"`
}

// ToolExecutor executes diagnostic tools against a cluster or canned source.
type ToolExecutor interface {
	Execute(ctx context.Context, toolName string, params map[string]string) (*ExecutionResult, error)
}

type SandboxedExecutor struct {
	registry *ToolRegistry
	timeout  time.Duration
	dryRun   bool
}

func NewSandboxedExecutor(registry *ToolRegistry, timeout time.Duration, dryRun bool) *SandboxedExecutor {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &SandboxedExecutor{
		registry: registry,
		timeout:  timeout,
		dryRun:   dryRun,
	}
}

func (e *SandboxedExecutor) Execute(ctx context.Context, toolName string, params map[string]string) (*ExecutionResult, error) {
	start := time.Now()

	// 1. Validate against allowlist and build safe command
	binary, args, err := e.registry.ValidateAndBuild(toolName, params)
	if err != nil {
		return &ExecutionResult{
			Error:    err.Error(),
			ExitCode: -1,
			Duration: time.Since(start),
		}, err
	}

	// 2. Binary whitelist check (kubectl only)
	if binary != "kubectl" {
		err := fmt.Errorf("DENIED: only 'kubectl' binary is permitted, got %q", binary)
		return &ExecutionResult{
			Error:    err.Error(),
			ExitCode: -1,
			Duration: time.Since(start),
		}, err
	}

	log.Printf("[Executor] Tool=%s Command=%s %v", toolName, binary, args)

	if e.dryRun {
		return &ExecutionResult{
			Stdout:   fmt.Sprintf("[DRY RUN] Would execute: %s %v", binary, args),
			ExitCode: 0,
			Duration: time.Since(start),
		}, nil
	}

	// 3. Direct subprocess execution with context timeout and nil stdin
	execCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = nil
	cmd.Env = childEnv(os.Environ())

	err = cmd.Run()

	result := &ExecutionResult{
		Stdout:   truncateOutput(stdout.String(), MaxOutputBytes),
		Stderr:   truncateOutput(stderr.String(), MaxOutputBytes),
		Duration: time.Since(start),
	}

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Sprintf("command timed out after %s", e.timeout)
			result.ExitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Error = result.Stderr
		} else {
			result.Error = err.Error()
			result.ExitCode = -1
		}
	}

	return result, nil
}

func truncateOutput(s string, maxSize int) string {
	if len(s) > maxSize {
		return s[:maxSize] + "\n... [output truncated at 1 MiB]"
	}
	return s
}

// childEnv is the environment kubectl gets: ours, minus our own GEMINI_* variables (the API key
// and the key-file path), which kubectl has no use for. Everything else is passed through,
// because kubectl login plugins may need cloud variables such as AWS_* or KUBECONFIG.
func childEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if strings.HasPrefix(strings.ToUpper(name), "GEMINI_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
