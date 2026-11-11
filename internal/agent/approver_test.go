package agent

import (
	"context"
	"testing"
)

func TestDenyAllApproverDeniesEverything(t *testing.T) {
	var ap Approver = DenyAllApprover{}
	for _, a := range []Action{
		{},
		{Kind: "restart_pod", Target: "prod/payment"},
		{Kind: "delete_pod", Target: "kube-system/etcd"},
	} {
		ok, reason := ap.Approve(context.Background(), a)
		if ok || reason == "" {
			t.Fatalf("expected denial with a reason for %+v", a)
		}
	}
}
