package agent

import "context"

// Action describes a fix that someone may want to apply in future.
// It is a description only; nothing in this program executes it.
type Action struct {
	Kind        string // e.g. "restart_pod"
	Target      string // e.g. "namespace/pod-name"
	Description string
}

// Approver decides whether an Action may run.
type Approver interface {
	Approve(ctx context.Context, a Action) (bool, string)
}

// DenyAllApprover is the default: every action is refused.
type DenyAllApprover struct{}

func (DenyAllApprover) Approve(ctx context.Context, a Action) (bool, string) {
	return false, "denied: this system is read-only"
}
