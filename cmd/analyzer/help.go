package main

import (
	"flag"
	"fmt"
	"os"
)

const helpText = `log-analyzer finds the likely root cause of an incident, from a log file or from a
Kubernetes cluster. The report is always shown on the screen. Saving it is optional.

USAGE
  analyzer --log-file FILE [options]       analyze a log file (no cluster needed)
  analyzer --namespace NAME [options]      investigate a Kubernetes cluster (needs kubectl)

EXAMPLES
  analyzer --log-file app.log                          try it: nothing is saved, nothing is sent
  analyzer --log-file app.log --output report.md       also keep a copy as a file
  analyzer --log-file app.log --git-repo ~/incidents   also commit it to a Git repository
  analyzer --namespace shop --service cart --api-key-file ~/.keys/gemini

WHAT TO ANALYZE
  --log-file FILE      a raw log file to analyze
  --namespace NAME     Kubernetes namespace to investigate (default: default)
  --service NAME       the service or workload to look at (optional)
  --symptom TEXT       what you are seeing, in your own words
  --since DURATION     how far back to read logs and events, for example 30m, 2h, 24h (default 1h)

WHERE THE REPORT GOES (all optional, the screen always shows it)
  --output FILE, -o FILE   also save the report as a markdown file. A folder (or a value ending
                           in /) gets a new, named file. Any other value is the file to write
  --git                    also commit the report to a Git repository (needs git installed)
  --git-repo DIR           the repository to use; this also turns --git on
                           (with --git alone: ~/log-analyzer-incidents)
  --branch                 commit on a separate branch for each incident

AI MODEL (optional: without a key, built-in rules run and nothing leaves your computer)
  --api-key-file FILE  read the API key from a file only you can read (best way)
  --provider NAME      gemini (default), openai (any OpenAI-compatible server) or anthropic
  --model NAME         the model name (only Gemini has a built-in default)
  --base-url URL       server address for openai or anthropic, for example Groq or Ollama
  --allow-http         allow plain http to a model server on another machine (not encrypted)
  --gemini-key-file F  same as --api-key-file; kept for existing setups
  --llm MODE           live (default), heuristic (rules only, nothing sent), record, replay

TESTING AND LIMITS
  --dry-run            print the kubectl commands instead of running them
  --timeout DURATION   stop after this long (default 10m)
  --recordings-dir D   where record and replay keep model answers (default recordings)
  --case-id ID         name for a recording

More: README.md and docs/CONFIG.md
`

// usage prints the grouped help text. It replaces Go's alphabetical flag list.
func usage() {
	fmt.Fprint(flag.CommandLine.Output(), helpText)
}

// noArguments says whether the program was started with nothing to do.
func noArguments() bool { return len(os.Args) == 1 }

const noArgumentsNote = `Nothing to analyze yet. Tell me what to look at, for example:

  analyzer --log-file app.log
  analyzer --namespace shop --api-key-file ~/.keys/gemini

Run with --help to see everything.
`
