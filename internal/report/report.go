// Package report turns the model's free-text answer into a structured incident report.
//
// The report has two views of the same data: Markdown for people and JSON for programs.
// Both come from one Report value, so they cannot disagree.
//
// It also runs one cheap, deterministic check: every line the model quotes as evidence
// (written between backticks) must really appear in the tool output that was collected.
// A quoted line that is not there is marked, and a fault answer whose quoted lines are all
// missing is not trusted: it becomes CANNOT_DETERMINE.
package report

import (
	"regexp"
	"strings"
	"time"

	"k8s-log-analyzer/internal/llm"
	"k8s-log-analyzer/internal/rootcause"
)

// Output is what one tool returned (already masked, the same text the model saw).
type Output struct {
	Tool string
	Text string
}

// Input is everything Build needs besides the model's answer.
type Input struct {
	ID, Namespace, Service, Symptom string
	Duration                        time.Duration
	Outputs                         []Output
}

// Evidence is one evidence item from the model's answer.
type Evidence struct {
	Tool     string `json:"tool,omitempty"`
	Line     string `json:"line"`
	Quoted   bool   `json:"quoted"`   // the model put the line between backticks
	Verified bool   `json:"verified"` // a quoted line was found in the tool output
}

// RuledOut is a cause the model says the data does not support.
type RuledOut struct {
	Category string `json:"category"`
	Why      string `json:"why"`
}

// Claim keeps what the model originally said when the report changed it.
type Claim struct {
	Category   string `json:"category"`
	Confidence string `json:"confidence,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

// Check summarises the evidence check.
type Check struct {
	Result   string `json:"result"` // verified, partial, failed, unquoted, not_needed
	Quoted   int    `json:"quoted"`
	Verified int    `json:"verified"`
}

// Report is the structured incident report.
type Report struct {
	ID              string     `json:"id"`
	Namespace       string     `json:"namespace"`
	Service         string     `json:"service,omitempty"`
	Symptom         string     `json:"symptom"`
	Category        string     `json:"category"`
	Confidence      string     `json:"confidence,omitempty"`
	Summary         string     `json:"summary"`
	Evidence        []Evidence `json:"evidence"`
	EvidenceCheck   Check      `json:"evidence_check"`
	RuledOut        []RuledOut `json:"ruled_out,omitempty"`
	Recommendations []string   `json:"recommendations,omitempty"`
	NextCommand     string     `json:"next_command,omitempty"`
	Notes           []string   `json:"notes,omitempty"`
	ModelClaim      *Claim     `json:"model_claim,omitempty"` // set only when the check changed the answer
	ToolCalls       int        `json:"tool_calls"`
	Tools           []string   `json:"tools,omitempty"`
	DurationSeconds int        `json:"duration_s"`
}

// minQuote is the shortest quoted line that counts. A one- or two-letter quote would match anything.
const minQuote = 4

var (
	spanRe     = regexp.MustCompile("`([^`\n]+)`")
	toolPrefix = regexp.MustCompile(`^\*{0,2}([a-z][a-z0-9_]*)\*{0,2}\s*:\s*`)
	bulletRe   = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+(.*)$`)
	ruledRe    = regexp.MustCompile(`^\s*([A-Z][A-Z_]{2,})\s*[:\-–—]\s*(.*)$`)
)

// Parsed is what Parse reads out of the model's text.
type Parsed struct {
	Category        string
	Confidence      string // HIGH, MEDIUM, LOW or ""
	Summary         string
	Evidence        []string
	RuledOut        []string
	Recommendations []string
	NextCommand     string
}

// Parse reads the sections of the model's answer. It accepts the plain form ("Evidence:") and
// the markdown form ("**Evidence:**" or "## Evidence"). Anything it cannot find stays empty.
func Parse(raw string) Parsed {
	p := Parsed{Category: finalCategory(raw)}
	section, inline := "", ""
	var lines []string
	flush := func() {
		if section != "" {
			p.assign(section, inline, lines)
		}
		section, inline, lines = "", "", nil
	}
	for _, l := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if name, rest, ok := header(l); ok {
			flush()
			section, inline = name, rest
			continue
		}
		if strings.Contains(strings.ToUpper(l), "ROOT_CAUSE_CATEGORY") {
			continue
		}
		if section != "" {
			lines = append(lines, l)
		}
	}
	flush()
	return p
}

var categoryLineRe = regexp.MustCompile(`(?i)ROOT_CAUSE_CATEGORY[:\s*]*([A-Z_]+)`)

// finalCategory returns the LAST valid ROOT_CAUSE_CATEGORY line: the prompt asks for it as the
// closing line, and an earlier mention (for example a quoted log line) must not outrank it.
// With no valid line it falls back to rootcause.Category.
func finalCategory(raw string) string {
	last := ""
	for _, m := range categoryLineRe.FindAllStringSubmatch(raw, -1) {
		c := strings.ToUpper(m[1])
		for _, k := range llm.AllCategories {
			if c == k {
				last = c
			}
		}
	}
	if last != "" {
		return last
	}
	return rootcause.Category(raw)
}

var sectionNames = []string{"root cause", "confidence", "evidence", "ruled out", "recommendations", "next command"}

// header says whether l starts a section, and returns the section name and any text after the colon.
func header(l string) (name, rest string, ok bool) {
	t := strings.TrimLeft(l, " \t>")
	heading := strings.HasPrefix(t, "#")
	t = strings.TrimLeft(t, "# \t*_")
	low := strings.ToLower(t)
	for _, n := range sectionNames {
		if !strings.HasPrefix(low, n) {
			continue
		}
		after := strings.TrimLeft(t[len(n):], "*_ \t")
		if strings.HasPrefix(after, ":") {
			return n, strings.Trim(after[1:], "*_ \t"), true
		}
		if heading && after == "" {
			return n, "", true
		}
	}
	return "", "", false
}

func (p *Parsed) assign(section, inline string, lines []string) {
	body := make([]string, 0, len(lines)+1)
	if inline != "" {
		body = append(body, inline)
	}
	body = append(body, lines...)
	switch section {
	case "root cause":
		p.Summary = strings.TrimSpace(strings.Join(nonEmpty(body), " "))
	case "confidence":
		up := strings.ToUpper(strings.Join(body, " "))
		for _, c := range []string{"HIGH", "MEDIUM", "LOW"} {
			if strings.Contains(up, c) {
				p.Confidence = c
				break
			}
		}
	case "evidence":
		p.Evidence = items(body)
	case "ruled out":
		p.RuledOut = items(body)
	case "recommendations":
		p.Recommendations = items(body)
	case "next command":
		t := strings.TrimSpace(strings.Join(nonEmpty(body), " "))
		if m := spanRe.FindStringSubmatch(t); m != nil {
			t = m[1]
		}
		p.NextCommand = strings.Trim(t, "` ")
	}
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// items splits list lines into entries. A line that is not a bullet continues the previous entry.
func items(body []string) []string {
	var out []string
	for _, l := range body {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if m := bulletRe.FindStringSubmatch(l); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
		} else if len(out) > 0 && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) {
			out[len(out)-1] += " " + strings.TrimSpace(l)
		} else {
			out = append(out, strings.TrimSpace(l))
		}
	}
	return out
}

func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// found says whether the quoted line appears in any tool output. Spaces and letter case do not
// matter. A quote that ends in "..." is a cut-off line and must match the start of a real line.
func found(quote string, outputs []Output) bool {
	q := strings.TrimSpace(quote)
	q = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(q, "..."), "…"))
	q = norm(q)
	if len([]rune(q)) < minQuote {
		return false
	}
	for _, o := range outputs {
		if strings.Contains(norm(o.Text), q) {
			return true
		}
	}
	return false
}

func lower(c string) string {
	switch c {
	case "HIGH":
		return "MEDIUM"
	case "MEDIUM":
		return "LOW"
	}
	return c
}

func isFault(cat string) bool {
	return cat != llm.CategoryNoFault && cat != llm.CategoryCannotDetermine
}

// Build parses the model's answer, checks its quoted evidence against the tool output and
// returns the report.
func Build(raw string, in Input) Report {
	p := Parse(raw)
	r := Report{
		ID: in.ID, Namespace: in.Namespace, Service: in.Service, Symptom: in.Symptom,
		Category: p.Category, Confidence: p.Confidence, Summary: p.Summary,
		Recommendations: p.Recommendations, NextCommand: p.NextCommand,
		Evidence: []Evidence{}, ToolCalls: len(in.Outputs), DurationSeconds: int(in.Duration.Round(time.Second) / time.Second),
	}
	seen := map[string]bool{}
	for _, o := range in.Outputs {
		if !seen[o.Tool] {
			seen[o.Tool] = true
			r.Tools = append(r.Tools, o.Tool)
		}
	}
	if r.Category == "" {
		r.Category = llm.CategoryCannotDetermine
		r.Notes = append(r.Notes, "The model did not name a root-cause category, so this is reported as "+llm.CategoryCannotDetermine+".")
	}
	if r.Summary == "" {
		r.Summary = "The model gave no root-cause text. See the evidence below."
		r.Notes = append(r.Notes, "The model's answer had no \"Root Cause\" text.")
	}
	for _, e := range p.Evidence {
		r.Evidence = append(r.Evidence, evidenceFrom(e, in.Outputs)...)
	}
	for _, s := range p.RuledOut {
		if m := ruledRe.FindStringSubmatch(strings.Trim(s, "*_ ")); m != nil {
			r.RuledOut = append(r.RuledOut, RuledOut{Category: m[1], Why: strings.TrimSpace(m[2])})
		} else {
			r.RuledOut = append(r.RuledOut, RuledOut{Why: s})
		}
	}
	for _, e := range r.Evidence {
		if e.Quoted {
			r.EvidenceCheck.Quoted++
			if e.Verified {
				r.EvidenceCheck.Verified++
			}
		}
	}
	r.applyCheck()
	return r
}

// evidenceFrom turns one evidence bullet into one item per quoted line, or one unquoted item.
func evidenceFrom(text string, outputs []Output) []Evidence {
	text = strings.TrimSpace(text)
	tool := ""
	if m := toolPrefix.FindStringSubmatch(strings.ReplaceAll(text, "`", "")); m != nil && !strings.HasPrefix(text, "`") {
		tool = m[1]
		text = strings.TrimSpace(toolPrefix.ReplaceAllString(text, ""))
	}
	spans := spanRe.FindAllStringSubmatch(text, -1)
	if len(spans) == 0 {
		return []Evidence{{Tool: tool, Line: text}}
	}
	var out []Evidence
	for _, s := range spans {
		out = append(out, Evidence{Tool: tool, Line: s[1], Quoted: true, Verified: found(s[1], outputs)})
	}
	return out
}

func (r *Report) applyCheck() {
	c := &r.EvidenceCheck
	switch {
	case !isFault(r.Category):
		c.Result = "not_needed"
	case c.Quoted == 0:
		c.Result = "unquoted"
		r.Confidence = lower(r.Confidence)
		r.Notes = append(r.Notes, "The model quoted no evidence lines, so nothing could be checked against the tool output. Confidence was lowered one level.")
	case c.Verified == c.Quoted:
		c.Result = "verified"
	case c.Verified == 0:
		c.Result = "failed"
		r.ModelClaim = &Claim{Category: r.Category, Confidence: r.Confidence, Summary: r.Summary}
		r.Notes = append(r.Notes, "The model said "+r.Category+", but none of its quoted evidence lines appear in the tool output, so the answer is not trusted.")
		r.Category = llm.CategoryCannotDetermine
		r.Confidence = "LOW"
		r.Summary = "Cannot tell. The model's answer could not be backed by the data that was collected."
	default:
		c.Result = "partial"
		r.Confidence = lower(r.Confidence)
		r.Notes = append(r.Notes, "Some quoted evidence lines are not in the tool output. Confidence was lowered one level.")
	}
}
