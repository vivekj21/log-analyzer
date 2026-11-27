package serve

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"k8s-log-analyzer/internal/agent"
)

// These patterns are the same ones the tools use for their own parameters, so a value that passes
// here is never rejected later, in the middle of an investigation.
var (
	namespaceRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	serviceRe   = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)
	sinceRe     = regexp.MustCompile(`^([0-9]+[smh])+$`)
)

const (
	defaultSymptom = "Something is wrong. No specific symptom was given."
	defaultSince   = "30m"
	maxSymptom     = 500
)

// createRequest is the JSON a client sends to start an investigation.
type createRequest struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Symptom   string `json:"symptom"`
	Since     string `json:"since"`
}

// problem is a rejected request, in words a person can act on.
type problem struct{ Message, Hint string }

func (p *problem) Error() string { return p.Message }

// cleanText makes untrusted text safe to put in a report and a prompt: control characters and
// line breaks become spaces, runs of spaces collapse, and the length is capped.
func cleanText(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			r = ' '
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if rs := []rune(out); len(rs) > max {
		out = string(rs[:max])
	}
	return out
}

// normalize checks a request and fills in the defaults. Only the namespace is required.
func normalize(in createRequest, allowed []string) (agent.Request, *problem) {
	ns := strings.TrimSpace(in.Namespace)
	if ns == "" {
		return agent.Request{}, &problem{"namespace is missing", `send at least {"namespace":"shop"}`}
	}
	if len(ns) > 63 || !namespaceRe.MatchString(ns) {
		return agent.Request{}, &problem{fmt.Sprintf("%q is not a valid namespace name", ns),
			"a namespace uses lowercase letters, digits and '-', for example shop or payments-prod"}
	}
	if len(allowed) > 0 && !contains(allowed, ns) {
		return agent.Request{}, &problem{fmt.Sprintf("namespace %q is not allowed on this server", ns),
			"allowed namespaces: " + strings.Join(allowed, ", ")}
	}
	svc := strings.TrimSpace(in.Service)
	if svc != "" && (len(svc) > 63 || !serviceRe.MatchString(svc)) {
		return agent.Request{}, &problem{fmt.Sprintf("%q is not a valid service name", svc),
			"use the name of the workload, for example cart or checkout-api"}
	}
	since := strings.TrimSpace(in.Since)
	if since == "" {
		since = defaultSince
	}
	if len(since) > 10 || !sinceRe.MatchString(since) {
		return agent.Request{}, &problem{fmt.Sprintf("%q is not a valid time window", since),
			"use a number and a unit: 30m, 2h, 90s"}
	}
	sym := cleanText(in.Symptom, maxSymptom)
	if sym == "" {
		sym = defaultSymptom
	}
	return agent.Request{Namespace: ns, Service: svc, Symptom: sym, Since: since}, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
