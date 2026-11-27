package report

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// categoryToken is the machine-readable line other tools read (the eval, Git). Free text must
// never contain it, or a log line quoted as evidence could be read as the answer.
var categoryToken = regexp.MustCompile(`(?i)ROOT_CAUSE_CATEGORY`)

func safe(s string) string { return categoryToken.ReplaceAllString(s, "ROOT CAUSE CATEGORY") }

// JSON returns the report as indented JSON.
func (r Report) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

func (r Report) checkLine() string {
	c := r.EvidenceCheck
	switch c.Result {
	case "verified":
		return fmt.Sprintf("%d of %d quoted lines found in the tool output", c.Verified, c.Quoted)
	case "partial", "failed":
		return fmt.Sprintf("only %d of %d quoted lines found in the tool output", c.Verified, c.Quoted)
	case "unquoted":
		return "not checked (the model quoted no lines)"
	default:
		if c.Quoted > 0 {
			return fmt.Sprintf("%d of %d quoted lines found in the tool output", c.Verified, c.Quoted)
		}
		return "not needed for this answer"
	}
}

// Markdown renders the report for people. The answer comes first; the last line is the
// machine-readable category.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Incident report: %s\n\n", r.Category)
	conf := r.Confidence
	if conf == "" {
		conf = "not stated"
	}
	fmt.Fprintf(&b, "**Confidence:** %s · **Evidence check:** %s\n\n", conf, r.checkLine())
	fmt.Fprintf(&b, "## Why\n%s\n", safe(r.Summary))

	if len(r.Evidence) > 0 {
		b.WriteString("\n## Evidence\n")
		for _, e := range r.Evidence {
			tool := ""
			if e.Tool != "" {
				tool = "`" + e.Tool + "`: "
			}
			switch {
			case !e.Quoted:
				fmt.Fprintf(&b, "- %s%s (not quoted, not checked)\n", tool, safe(e.Line))
			case e.Verified:
				fmt.Fprintf(&b, "- %s`%s` ✔\n", tool, safe(e.Line))
			default:
				fmt.Fprintf(&b, "- %s`%s` ✘ not found in the tool output\n", tool, safe(e.Line))
			}
		}
	}
	if len(r.RuledOut) > 0 {
		b.WriteString("\n## Ruled out\n")
		for _, x := range r.RuledOut {
			if x.Category != "" {
				fmt.Fprintf(&b, "- %s: %s\n", x.Category, safe(x.Why))
			} else {
				fmt.Fprintf(&b, "- %s\n", safe(x.Why))
			}
		}
	}
	if len(r.Recommendations) > 0 {
		b.WriteString("\n## What to do\n")
		for i, s := range r.Recommendations {
			fmt.Fprintf(&b, "%d. %s\n", i+1, safe(s))
		}
	}
	if r.NextCommand != "" {
		b.WriteString("\n## Next command (you run it; this tool never does)\n")
		fmt.Fprintf(&b, "`%s`\n", safe(r.NextCommand))
	}
	if len(r.Notes) > 0 {
		b.WriteString("\n## Notes\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", safe(n))
		}
	}
	b.WriteString("\n---\n")
	fmt.Fprintf(&b, "Investigation %s · namespace %s", r.ID, r.Namespace)
	if r.Service != "" {
		fmt.Fprintf(&b, " · service %s", r.Service)
	}
	fmt.Fprintf(&b, " · %ds · %d tool calls", r.DurationSeconds, r.ToolCalls)
	if len(r.Tools) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(r.Tools, ", "))
	}
	b.WriteString(" · data was masked before the model saw it\n")
	if r.Symptom != "" {
		fmt.Fprintf(&b, "Symptom: %s\n", safe(r.Symptom))
	}
	fmt.Fprintf(&b, "\nROOT_CAUSE_CATEGORY: %s\n", r.Category)
	return b.String()
}
