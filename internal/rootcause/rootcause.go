// Package rootcause pulls the AI's conclusion (category, summary) out of its
// free-text answer and maps a category to a severity.
package rootcause

import (
	"regexp"
	"strings"

	"k8s-log-analyzer/internal/llm"
)

var (
	categoryRe = regexp.MustCompile(`(?i)ROOT_CAUSE_CATEGORY[:\s*]*([A-Z_]+)`)
	summaryRe  = regexp.MustCompile(`(?im)^[\s*#>_-]*root cause[\s*_]*:[\s*_]*(.+)$`)
	known      = llm.AllCategories
)

// Category returns the root-cause category in text, or "" if none is found.
func Category(text string) string {
	if m := categoryRe.FindStringSubmatch(text); len(m) > 1 {
		return strings.ToUpper(strings.TrimSpace(m[1]))
	}
	for _, c := range known {
		if strings.Contains(text, c) {
			return c
		}
	}
	return ""
}

// Summary returns the "Root Cause:" line, or the first non-empty line, capped at 300 chars.
func Summary(text string) string {
	if m := summaryRe.FindStringSubmatch(text); len(m) > 1 {
		return clip(strings.Trim(strings.TrimSpace(m[1]), "*_ "))
	}
	for _, l := range strings.Split(text, "\n") {
		if l = strings.Trim(strings.TrimSpace(l), "#*>_ -"); l != "" {
			return clip(l)
		}
	}
	return ""
}

// Severity maps a category to P0..P3. Unknown or empty category gives P2.
func Severity(category string) string {
	switch category {
	case llm.CategoryOOMKilled, llm.CategoryCrashLoopBadConfig, llm.CategoryDBLock:
		return "P1"
	case llm.CategoryNoFault:
		return "P3"
	}
	return "P2"
}

func clip(s string) string {
	r := []rune(s)
	if len(r) > 300 {
		return string(r[:300])
	}
	return s
}
