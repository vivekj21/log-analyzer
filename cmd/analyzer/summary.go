package main

import (
	"fmt"
	"strings"
)

// gitState says what happened to the optional Git commit.
type gitState int

const (
	gitNotAsked gitState = iota
	gitCommitted
	gitFailed
)

// runSummary is the short "where things went" note printed at the end of a run.
// It goes to standard error, so `analyzer ... > report.md` still holds only the report.
// It only says what is true for this run, and mentions an option only when that option was not used.
type runSummary struct {
	// Model service: SentToModel is true when masked log text was sent to Provider/Model.
	SentToModel     bool
	ModelFailed     bool // the model service returned an error
	Provider, Model string

	// File output (--output).
	OutputAsked  bool
	OutputPath   string // set when the file was saved
	OutputFailed bool

	// Git (--git / --git-repo).
	Git       gitState
	GitDetail string // for example "incident.md (commit abc1234)"
}

const summaryRule = "----------------------------------------------------------------------"

func (s runSummary) String() string {
	var b strings.Builder
	b.WriteString("\n" + summaryRule + "\nWhere things went\n")
	b.WriteString("  Report : shown above (this is the main result)\n")

	switch {
	case s.OutputFailed:
		b.WriteString("  File   : NOT saved (see the warning above)\n")
	case s.OutputPath != "":
		fmt.Fprintf(&b, "  File   : saved to %s\n", s.OutputPath)
	case !s.OutputAsked:
		b.WriteString("  File   : not saved. To keep a copy, add: --output report.md\n")
	}

	switch s.Git {
	case gitCommitted:
		fmt.Fprintf(&b, "  Git    : committed %s\n", s.GitDetail)
	case gitFailed:
		b.WriteString("  Git    : NOT committed (see the warning above)\n")
	default:
		b.WriteString("  Git    : not committed. To commit it, add: --git-repo ~/incidents\n")
	}

	switch {
	case s.SentToModel && s.ModelFailed:
		fmt.Fprintf(&b, "  Privacy: secrets were masked, then the analysis tried to send the log text to %s (%s)\n           and that service failed. The rule-based part is in the report. To avoid this, add: --llm heuristic\n", s.Provider, s.Model)
	case s.SentToModel:
		fmt.Fprintf(&b, "  Privacy: secrets were masked, then the log text was sent to %s (%s).\n           To keep everything on this computer, add: --llm heuristic\n", s.Provider, s.Model)
	default:
		b.WriteString("  Privacy: the analysis ran on this computer. No log text was sent anywhere.\n")
	}
	b.WriteString("All options: run with --help\n" + summaryRule + "\n")
	return b.String()
}
