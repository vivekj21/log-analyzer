package llm

import (
	"context"
	"strings"
	"testing"

	"k8s-log-analyzer/internal/protocol"
)

// The pod table is all the rule-based model sees first. A pod that was killed for memory can
// look like any of these when it is caught, so none of them may depend on a failure word.
const (
	tblRunningRestarted = "NAME                   READY   STATUS    RESTARTS      AGE\noom-7d9f8c6b5-abcde    1/1     Running   3 (40s ago)   2m\n"
	tblNotReady         = "NAME                   READY   STATUS    RESTARTS   AGE\nweb-7d9f8c6b5-abcde    0/1     Running   0          2m\n"
	tblContainerCreate  = "NAME                   READY   STATUS              RESTARTS   AGE\nweb-7d9f8c6b5-abcde    0/1     ContainerCreating   0          2s\n"
	tblHealthy          = "NAME                   READY   STATUS    RESTARTS   AGE\nweb-7d9f8c6b5-abcde    1/1     Running   0          2m\n"
)

func TestHeuristicInvestigatesPodFromTableAlone(t *testing.T) {
	h := NewHeuristicModel()
	for name, tbl := range map[string]string{
		"running but restarted": tblRunningRestarted,
		"running but not ready": tblNotReady,
		"still starting":        tblContainerCreate,
	} {
		res, err := h.Analyze(context.Background(), "t", "Symptom: pod keeps going away\n"+tbl)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, fc := range res.FunctionCalls {
			names = append(names, fc.Name)
		}
		if !res.HasToolCalls || strings.Join(names, ",") != "describe_pod,get_pod_logs" {
			t.Errorf("%s: want describe_pod and get_pod_logs, got calls %v text %q", name, names, res.TextResponse)
		}
	}
}

func TestHeuristicHealthyTableDoesNotInvestigate(t *testing.T) {
	res, err := NewHeuristicModel().Analyze(context.Background(), "t", "Symptom: just checking\n"+tblHealthy)
	if err != nil {
		t.Fatal(err)
	}
	if res.HasToolCalls || !strings.Contains(res.TextResponse, "ROOT_CAUSE_CATEGORY: CANNOT_DETERMINE") {
		t.Errorf("healthy table: want CANNOT_DETERMINE and no tool calls, got %+v", res)
	}
}

// The cause of an out-of-memory kill is in the pod description ("Last State"), not in the logs.
func TestHeuristicFindsOOMInPodDescription(t *testing.T) {
	h := NewHeuristicModel()
	ctx := context.Background()
	if _, err := h.Analyze(ctx, "t", "Symptom: pod keeps going away\n"+tblRunningRestarted); err != nil {
		t.Fatal(err)
	}
	res, err := h.SendToolResults(ctx, "t", []protocol.ToolCallResult{
		{ToolName: "describe_pod", Result: "Last State:     Terminated\n  Reason:       OOMKilled\n  Exit Code:    137\n"},
		{ToolName: "get_pod_logs", Result: "stress: info: [1] dispatching hogs: 0 cpu, 0 io, 1 vm, 0 hdd\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.TextResponse, "ROOT_CAUSE_CATEGORY: "+CategoryOOMKilled) {
		t.Errorf("want OOM_KILLED, got:\n%s", res.TextResponse)
	}
}

func TestSuspectPod(t *testing.T) {
	cases := []struct {
		name, text, pod string
		suspicious      bool
	}{
		{"restart count with time", tblRunningRestarted, "oom-7d9f8c6b5-abcde", true},
		{"not ready", tblNotReady, "web-7d9f8c6b5-abcde", true},
		{"healthy", tblHealthy, "web-7d9f8c6b5-abcde", false},
		{"no table", "nothing here", "", false},
		{"bad pod beats restarted pod", tblRunningRestarted + "bad-6c7d8f-xyz12    0/1     CrashLoopBackOff   4    5m\n", "bad-6c7d8f-xyz12", true},
	}
	for _, c := range cases {
		pod, sus := suspectPod(c.text)
		if pod != c.pod || sus != c.suspicious {
			t.Errorf("%s: got (%q,%v) want (%q,%v)", c.name, pod, sus, c.pod, c.suspicious)
		}
	}
}
