package llm

import "testing"

func TestPickPod(t *testing.T) {
	tbl := "NAME                        READY   STATUS             RESTARTS   AGE\n" +
		"ok-demo-5d8f7c9b6-abcde     1/1     Running            0          5m\n" +
		"config-demo-6c7d8f-xyz12    0/1     CrashLoopBackOff   4          5m\n"
	if got := pickPod("some text\n" + tbl + "more"); got != "config-demo-6c7d8f-xyz12" {
		t.Fatalf("got %q", got)
	}
	if got := pickPod("no table here, pod in CrashLoopBackOff right after deploy"); got != "" {
		t.Fatalf("got %q", got)
	}
}
