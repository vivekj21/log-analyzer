# Log Analyzer

**An AI on-call helper for Kubernetes. It reads your logs (or investigates a live cluster), finds the likely root cause, and files an incident report in Git. It can look at your cluster. It cannot change it.**

> **Status:** early-stage, read-only prototype. Tested on 8 hand-written attack cases, not yet at scale on a real cluster. Read [Limits](#limits) before relying on it.

## In 30 seconds

| You give it | It does | You get |
|---|---|---|
| A log file, or a namespace and a symptom | Collects evidence and asks an AI for the root cause | A markdown report with a severity (P0 to P3) and the root cause, committed to Git |

- **Safe by design:** read-only, 10 fixed tools, every request checked, secrets masked. [Details](docs/SECURITY.md)
- **Cheap to try:** reading a log file needs no API key.
- **Honest about limits:** the AI can be wrong, so a human should review every report.

*Root cause* means the main reason something broke. A *namespace* is a named section of a Kubernetes cluster.

## Quick start

Requires Go 1.24 or newer and `git`. Live mode also needs `kubectl` access and a `GEMINI_API_KEY`.

**1. Analyze a log file** (no cluster, no API key):

```bash
go run ./cmd/analyzer/main.go --log-file=/path/to/app.log
```

The report is saved in `~/log-analyzer-incidents/.log-analyzer/incidents/`, which is its own small Git repo. Change the place with `--git-repo`.

**2. Investigate a live cluster:**

```bash
export GEMINI_API_KEY="your-key"
go run ./cmd/analyzer/main.go --namespace=production --service=payment-service \
  --symptom="HTTP 504 and pods crashing" --since=2h
```

Add `--dry-run` to print the `kubectl` commands instead of running them.

**3. Run it inside Kubernetes** (new, not yet tested on a real cluster):

```bash
make docker-build            # push the image to a registry your cluster can reach
kubectl create secret generic gemini-key --from-literal=GEMINI_API_KEY="your-key"
helm install inv1 deploy/helm/log-analyzer --set gemini.existingSecret=gemini-key \
  --set investigation.namespace=production --set investigation.service=payment-service
kubectl logs -f job/inv1
```

The chart grants read-only access and not the secrets ConfigMap by default.

## How it works

An *agent* is a program that does one job. Four of them work in a line:


- **Producer:** starts the investigation and keeps order.
- **Fetcher:** collects evidence using 10 fixed read-only tools, with no shell.
- **Analyzer:** the AI (Gemini). It reads the evidence, asks for more if needed, and picks the cause.
- **GitOps Scribe:** writes `YYYY-MM-DD-<severity>-<slug>.md` and commits it as `incident(P1): <title>`. Add `--branch` to use a separate branch.

The AI sorts causes into six groups: out of memory, crash loop from bad settings, image pull failure, failing health check, database lock, or no fault. Log-file mode recognizes patterns such as out-of-memory, panics, crash loops, 5xx cascades, database locks, full disks, expired certificates and login attacks.

## Why it is safe

| Layer | What it does |
|---|---|
| Untrusted-data delimiters | The AI is told log text is data, never orders |
| Fixed tools | 10 read-only tools; a test fails if a write tool is added |
| Parameter checks | Patterns, length limits, no shell metacharacters |
| Policy | Right namespace only, no secrets ConfigMap by default, at most 12 tool calls, no repeats |
| Redaction | Secrets are masked before anything is saved |
| Cost limits | Token and call limits, and a circuit breaker after repeated failures |
| No write path | Fixes are text suggestions only; an approver that denies everything is the default |

More: [docs/SECURITY.md](docs/SECURITY.md). Tested with 8 attack cases; the current model resisted them even without the layers, so the layers are insurance, proven by deterministic code tests.

## Configuration

| Setting | Meaning |
|---|---|
| `GEMINI_API_KEY` | Gemini key (environment variable, never in code) |
| `GEMINI_MODEL` or `--model` | Model name; the default is in `internal/llm/defaults.go` |
| `LOG_ANALYZER_MAX_CALLS`, `LOG_ANALYZER_MAX_TOKENS`, `LOG_ANALYZER_MAX_TOTAL_TOKENS` | Cost limits; 0 turns one off |

All settings: [docs/CONFIG.md](docs/CONFIG.md).

## Limits

- **Small test:** 8 hand-written attacks, one run, one small model.
- **The AI can be wrong.** A human should review every report before acting.
- **Masking is pattern-based.** Unknown secret formats can slip through.
- **Logs leave your environment.** They are sent to the Gemini API.
- **Same-namespace data stays readable.** Run with a read-only Kubernetes account.
- **It suggests, never fixes.** You apply any fix yourself.
- **Reports are stored outside the repo** and are not backed up unless you push that folder.
- **Model names expire.** Keep `--model` current; free Gemini tiers have low limits.

Not enterprise-ready yet: see [docs/ROADMAP.md](docs/ROADMAP.md).

## Develop

```bash
make ci           # the full check, same as GitHub; free, needs no key
make hooks        # run it before every git push
make check-model  # can my key use this model?
make eval-live    # re-run the attack tests on the current model (costs money)
```

## Project layout
cmd/analyzer the program
cmd/eval attack tests against recorded or live AI answers
internal/agent producer, fetcher, analyzer, git agent
internal/toolbox the 10 read-only tools, validation, policy
internal/redact secret masking
internal/llm Gemini client, recorder, cost limits
internal/protocol messages between agents
internal/ttlmap map whose idle entries expire
evals/ attack cases and results
deploy/helm Helm chart
docs/ SECURITY.md, CONFIG.md, ROADMAP.md
testdata/ sample logs
incidents/ reports written by early runs
