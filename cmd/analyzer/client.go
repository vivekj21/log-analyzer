package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Options that make no sense when a running server does the work.
var notWithServer = []string{
	"log-file", "provider", "base-url", "allow-http", "api-key-file", "gemini-key-file", "model",
	"llm", "recordings-dir", "dry-run", "case-id", "git", "git-repo", "branch",
}

// normalizeServer turns what a person types into a base address: "127.0.0.1:8080", ":8080" and
// "localhost:8080" get http://, and a trailing slash is dropped.
func normalizeServer(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, ":") {
		s = "127.0.0.1" + s
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%q is not a server address. Use something like http://127.0.0.1:8080 or just 127.0.0.1:8080", s)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// usedWithServer returns the flags the person gave that cannot be used together with --server.
func usedWithServer(set map[string]bool) []string {
	var bad []string
	for _, f := range notWithServer {
		if set[f] {
			bad = append(bad, "--"+f)
		}
	}
	return bad
}

// explainReply turns an error answer from the server into words that say what to do.
func explainReply(code int, body []byte, retryAfter string) string {
	switch code {
	case http.StatusUnauthorized:
		return "The server needs a token. Give --token-file FILE (or set LOG_ANALYZER_TOKEN_FILE)."
	case http.StatusTooManyRequests:
		wait := retryAfter
		if wait == "" {
			wait = "30"
		}
		return "The server is busy with other investigations. Try again in about " + wait + " seconds."
	}
	var e struct{ Error, Hint string }
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		msg := "The server answered " + fmt.Sprint(code) + ": " + e.Error
		if e.Hint != "" {
			msg += "\n  " + e.Hint
		}
		return msg
	}
	return fmt.Sprintf("The server answered %d: %s", code, strings.TrimSpace(string(body)))
}

type clientArgs struct {
	server, tokenFile, output          string
	namespace, service, symptom, since string
	timeout                            time.Duration
	set                                map[string]bool
}

// runClient asks a running `log-analyzer serve` for a report, with the same flags as a local run.
// It returns the exit code.
func runClient(a clientArgs) int {
	if bad := usedWithServer(a.set); len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "These options are not used with --server, because the server does the work: %s\nRemove them, or run without --server.\n", strings.Join(bad, ", "))
		return 2
	}
	base, err := normalizeServer(a.server)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	token := loadToken(a.tokenFile)

	// Only what the person gave is sent. The server fills in the rest with its own defaults.
	req := map[string]string{"namespace": a.namespace}
	if a.service != "" {
		req["service"] = a.service
	}
	if a.set["symptom"] {
		req["symptom"] = a.symptom
	}
	if a.set["since"] {
		req["since"] = a.since
	}
	body, _ := json.Marshal(req)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, cancelT := context.WithTimeout(ctx, a.timeout)
	defer cancelT()

	hr, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/investigations?wait=true&format=md", bytes.NewReader(body))
	hr.Header.Set("Content-Type", "application/json")
	if token != "" {
		hr.Header.Set("Authorization", "Bearer "+token)
	}
	fmt.Fprintf(os.Stderr, "Asking the server at %s about namespace %s ...\n", base, a.namespace)

	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			fmt.Fprintf(os.Stderr, "No report within %s. Raise --timeout, or narrow it with --service.\n", a.timeout)
			return 1
		case errors.Is(ctx.Err(), context.Canceled):
			fmt.Fprintln(os.Stderr, "Interrupted.")
			return 130
		}
		fmt.Fprintf(os.Stderr, "Cannot reach the server at %s.\nIs it running? Start it with:  log-analyzer serve\n", base)
		return 1
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, explainReply(resp.StatusCode, out, resp.Header.Get("Retry-After")))
		return 1
	}
	fmt.Print(string(out))
	if a.output != "" {
		saveReport(a.output, "k8s "+a.namespace+" "+a.service, string(out))
	}
	return 0
}
