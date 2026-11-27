package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// startServe runs `log-analyzer serve` and returns its address once it says it is ready.
func startServe(t *testing.T, args ...string) (base string, logs func() string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("these tests use a POSIX PATH")
	}
	cmd := exec.Command(binPath, append([]string{"serve", "--dry-run", "--listen", "127.0.0.1:0"}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + t.TempDir(), "HOME=" + t.TempDir()}
	pipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	var mu sync.Mutex
	var all bytes.Buffer
	ready := make(chan string, 1)
	re := regexp.MustCompile(`serve is ready: (http://\S+)`)
	go func() {
		sc := bufio.NewScanner(pipe)
		for sc.Scan() {
			mu.Lock()
			all.WriteString(sc.Text() + "\n")
			mu.Unlock()
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				select {
				case ready <- m[1]:
				default:
				}
			}
		}
	}()
	logs = func() string { mu.Lock(); defer mu.Unlock(); return all.String() }
	select {
	case base = <-ready:
	case <-time.After(20 * time.Second):
		t.Fatalf("serve did not become ready:\n%s", logs())
	}
	return base, logs
}

func call(t *testing.T, method, url, body string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// The first-five-minutes path from the README: start, one curl, a report.
func TestServeQuickStartWorksWithNoSetup(t *testing.T) {
	base, logs := startServe(t)
	code, body := call(t, "POST", base+"/v1/investigations?wait=true", `{"namespace":"shop"}`, nil)
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil || code != 200 {
		t.Fatalf("%d %s\n%s", code, body, logs())
	}
	for _, k := range []string{"id", "category", "confidence", "summary", "evidence_check"} {
		if _, ok := m[k]; !ok {
			t.Errorf("report JSON lacks %q: %s", k, body)
		}
	}
	if m["status"] != "done" && m["status"] != "partial" {
		t.Fatalf("status %v: %s", m["status"], body)
	}
	if code, home := call(t, "GET", base+"/", "", nil); code != 200 || !strings.Contains(home, "?wait=true") {
		t.Fatalf("home page: %d %s", code, home)
	}
	if !strings.Contains(logs(), "built-in rules") {
		t.Errorf("with no key, the start message must say the built-in rules are used:\n%s", logs())
	}
}

func TestServeRefusesTheNetworkWithoutAToken(t *testing.T) {
	r := run(t, t.TempDir(), t.TempDir(), t.TempDir(), "serve", "--listen", ":0", "--llm", "heuristic")
	if r.code == 0 || !strings.Contains(r.stderr, "Refusing to listen") || !strings.Contains(r.stderr, "--token-file") {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	if strings.Contains(r.stderr, "panic") {
		t.Fatal("must be a clear message, not a crash")
	}
}

func TestServeTokenFileIsEnforcedAndShortTokensAreRefused(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "short")
	if err := os.WriteFile(short, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run(t, dir, t.TempDir(), t.TempDir(), "serve", "--token-file", short, "--llm", "heuristic")
	if r.code == 0 || !strings.Contains(r.stderr, "too short") {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	good := filepath.Join(dir, "token")
	if err := os.WriteFile(good, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, _ := startServe(t, "--token-file", good)
	if code, _ := call(t, "GET", base+"/v1/investigations", "", nil); code != 401 {
		t.Fatalf("without the token: %d", code)
	}
	if code, _ := call(t, "GET", base+"/v1/investigations", "", map[string]string{"Authorization": "Bearer 0123456789abcdef0123456789abcdef"}); code != 200 {
		t.Fatalf("with the token: %d", code)
	}
}

// Every serve flag must be described in `serve --help`.
func TestMainHelpMentionsServe(t *testing.T) {
	r := run(t, t.TempDir(), t.TempDir(), t.TempDir(), "--help")
	if !strings.Contains(r.stdout+r.stderr, "log-analyzer serve") {
		t.Fatal("the main help must tell people that serve exists")
	}
}

func TestServeHelpListsEveryFlag(t *testing.T) {
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatal(err)
	}
	names := regexp.MustCompile(`fs\.(?:String|Bool|Duration|Int)\("([a-z-]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(names) < 6 {
		t.Fatalf("found only %d serve flags", len(names))
	}
	r := run(t, t.TempDir(), t.TempDir(), t.TempDir(), "serve", "--help")
	help := r.stdout + r.stderr
	for _, m := range names {
		if !strings.Contains(help, "--"+m[1]) {
			t.Errorf("serve flag --%s is not described in serve --help", m[1])
		}
	}
	if !strings.Contains(help, "curl -X POST") {
		t.Error("serve --help must show the first command to try")
	}
}
