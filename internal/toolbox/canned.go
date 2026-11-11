package toolbox

import (
	"context"
	"fmt"
	"time"
)

// CannedExecutor serves predefined tool outputs from a map or JSON definition,
// allowing tests and evaluations to execute without a Kubernetes cluster.
type CannedExecutor struct {
	outputs map[string]string
}

// NewCannedExecutor creates a CannedExecutor with the provided canned outputs.
func NewCannedExecutor(outputs map[string]string) *CannedExecutor {
	if outputs == nil {
		outputs = make(map[string]string)
	}
	return &CannedExecutor{outputs: outputs}
}

// Execute returns canned output for the requested tool.
func (c *CannedExecutor) Execute(ctx context.Context, toolName string, params map[string]string) (*ExecutionResult, error) {
	if out, ok := c.outputs[toolName]; ok {
		return &ExecutionResult{
			Stdout:   out,
			ExitCode: 0,
			Duration: 5 * time.Millisecond,
		}, nil
	}

	return &ExecutionResult{
		Stdout:   fmt.Sprintf("[CANNED] No output recorded for tool: %s", toolName),
		ExitCode: 0,
		Duration: 1 * time.Millisecond,
	}, nil
}
