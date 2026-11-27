package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:8080":         "http://127.0.0.1:8080",
		":8080":                  "http://127.0.0.1:8080",
		"localhost:9":            "http://localhost:9",
		"http://host:8080/":      "http://host:8080",
		"https://la.example.com": "https://la.example.com",
	} {
		got, err := normalizeServer(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "http://", "ht!tp://x"} {
		if _, err := normalizeServer(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestExplainReply(t *testing.T) {
	if m := explainReply(401, nil, ""); !strings.Contains(m, "--token-file") {
		t.Error(m)
	}
	if m := explainReply(429, nil, "12"); !strings.Contains(m, "12 seconds") {
		t.Error(m)
	}
	if m := explainReply(400, []byte(`{"error":"namespace is missing","hint":"send one"}`), ""); !strings.Contains(m, "namespace is missing") || !strings.Contains(m, "send one") {
		t.Error(m)
	}
	if m := explainReply(500, []byte("Investigation x failed: boom\n"), ""); !strings.Contains(m, "boom") {
		t.Error(m)
	}
}

func hostport(base string) string { return strings.TrimPrefix(base, "http://") }

// The same flags as a local run, plus --server: the report is printed and nothing else is needed.
func TestClientAsksTheServerWithTheSameFlags(t *testing.T) {
	base, _ := startServe(t)
	e := newEnv(t)
	r := run(t, e.work, e.home, e.noGit, "--server", hostport(base), "--namespace", "shop")
	if r.code != 0 || !strings.Contains(r.stdout, "# Incident report:") || !strings.Contains(r.stderr, "Asking the server at "+base) {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "Something is wrong") {
		t.Errorf("without --symptom the server's neutral default must be used, not the local CLI's guess:\n%s", r.stdout)
	}
	out := filepath.Join(t.TempDir(), "r.md")
	r = run(t, e.work, e.home, e.noGit, "--server", hostport(base), "--namespace", "shop", "--service", "cart", "--symptom", "checkout restarts", "--since", "2h", "--output", out)
	if r.code != 0 || !strings.Contains(r.stdout, "checkout restarts") || !strings.Contains(r.stdout, "service cart") {
		t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
	}
	if b, err := os.ReadFile(out); err != nil || !strings.Contains(string(b), "# Incident report:") {
		t.Fatalf("--output must save the report: %v", err)
	}
}

func TestClientErrorsSayWhatToDo(t *testing.T) {
	base, _ := startServe(t)
	e := newEnv(t)
	r := run(t, e.work, e.home, e.noGit, "--server", hostport(base), "--namespace", "Bad NS")
	if r.code != 1 || !strings.Contains(r.stderr, "not a valid namespace") || !strings.Contains(r.stderr, "lowercase") {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	r = run(t, e.work, e.home, e.noGit, "--server", "127.0.0.1:1", "--namespace", "shop")
	if r.code != 1 || !strings.Contains(r.stderr, "Cannot reach the server") || !strings.Contains(r.stderr, "log-analyzer serve") {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	r = run(t, e.work, e.home, e.noGit, "--server", "ht!tp://x", "--namespace", "shop")
	if r.code != 2 || !strings.Contains(r.stderr, "not a server address") {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
}

// Options that only make sense for a local run are refused, never silently ignored: a person who
// gives --api-key-file must not believe that their key is being used.
func TestClientRefusesLocalOnlyOptions(t *testing.T) {
	e := newEnv(t)
	r := run(t, e.work, e.home, e.noGit, "--server", "127.0.0.1:8080", "--namespace", "shop", "--api-key-file", "k", "--llm", "live", "--log-file", e.logFile)
	if r.code != 2 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	for _, f := range []string{"--api-key-file", "--llm", "--log-file", "the server does the work"} {
		if !strings.Contains(r.stderr, f) {
			t.Errorf("message must name %q:\n%s", f, r.stderr)
		}
	}
}

func TestClientWithToken(t *testing.T) {
	dir := t.TempDir()
	tok := filepath.Join(dir, "token")
	if err := os.WriteFile(tok, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(dir, "wrong")
	if err := os.WriteFile(wrong, []byte("ffffffffffffffffffffffffffffffff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, _ := startServe(t, "--token-file", tok)
	e := newEnv(t)
	r := run(t, e.work, e.home, e.noGit, "--server", hostport(base), "--namespace", "shop")
	if r.code != 1 || !strings.Contains(r.stderr, "needs a token") {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	r = run(t, e.work, e.home, e.noGit, "--server", hostport(base), "--namespace", "shop", "--token-file", wrong)
	if r.code != 1 || !strings.Contains(r.stderr, "token") {
		t.Fatalf("a wrong token must fail with a token message: exit %d\n%s", r.code, r.stderr)
	}
	r = run(t, e.work, e.home, e.noGit, "--server", hostport(base), "--namespace", "shop", "--token-file", tok)
	if r.code != 0 || !strings.Contains(r.stdout, "# Incident report:") {
		t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
	}
}
