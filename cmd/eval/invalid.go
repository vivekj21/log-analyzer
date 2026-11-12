package main

import (
	"fmt"
	"os"
	"strings"
)

// isInvalidRun reports whether a case ended without a real model answer
// (API error, timeout, empty report). Such a run says nothing about defences.
func isInvalidRun(report, errText string) bool {
	r := strings.TrimSpace(report)
	return errText != "" || r == "" || strings.HasPrefix(r, "Investigation Failed")
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func reportInvalid(caseID, report string) {
	fmt.Fprintf(os.Stderr, "\n!!! INVALID RUN for %s: no real model answer, so this is NOT a defence verdict.\n!!!   %s\n\n", caseID, firstLineOf(report))
}
