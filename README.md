<h1 align="center">Log Analyzer</h1>

<p align="center">
<b>An agentic AI on-call engineer for Kubernetes.</b><br/>
It investigates an incident on its own, explains the root cause, and files the report in Git.
</p>

<p align="center">
<a href="https://github.com/vivekj21/log-analyzer/actions/workflows/ci.yml"><img src="https://github.com/vivekj21/log-analyzer/actions/workflows/ci.yml/badge.svg" alt="ci status"/></a>
<a href="https://github.com/vivekj21/log-analyzer/actions/workflows/e2e.yml"><img src="https://github.com/vivekj21/log-analyzer/actions/workflows/e2e.yml/badge.svg" alt="end-to-end status"/></a>
</p>

> **Built to be safe: read-only.** It can look at your cluster to diagnose problems, but it cannot change anything in it. Even if the AI is wrong or tricked, it can only run a short list of read-only commands.

## What it does

This tool finds the root cause when your app on Kubernetes has errors or is failing. Its AI agents read your logs and cluster details, then tell you the most likely root cause and how urgent it is (P0 is the most urgent, P3 the least). A short report is saved in Git.

You can also give it just a log file. Without an AI service, built-in rules do a simpler version of the same job.

## Why trust it

- **It is made to be safe.**
  - It cannot change your cluster. It only runs read-only commands from a short allowed list.
  - Your data stays with you. Nothing is sent to an AI service unless you set one up, and you pick which one. Secrets and email addresses are masked first. See [docs/DATA-FLOW.md](docs/DATA-FLOW.md).
  - It was tested against prompt-injection tricks (so far on Gemini only).
- **It publishes its score,** including where it fails: [accuracy/RESULTS.md](accuracy/RESULTS.md).

## See it work

```mermaid
flowchart LR
    IN(["Log file or<br/>live cluster"]) --> PR["Producer<br/>runs the investigation loop"]
    PR <--> FE["Fetcher<br/>10 read-only tools<br/>checked, no shell"]
    PR <--> AN["Analyzer<br/>the AI model decides what to look at next"]
    PR --> GS["GitOps Scribe<br/>writes the report"]
    GS --> OUT[("Git repo<br/>incident report")]
```

Real output from a run on a 216 KB Linux syslog (log-file mode):

```
LOG ANALYSIS COMPLETE - ISSUE COMMITTED TO GIT
Incident File  : .log-analyzer/incidents/2026-11-11-P1-security-anomaly-auth-surge-in.md
Severity       : P1
Root Cause     : Brute force credential stuffing attack, automated SSH scanning, or invalid authentication signatures.
```

## Why it is agentic

An *agent* is a program that decides and acts, not just answers. Here, four of them work together:

```mermaid
sequenceDiagram
    participant AI as Analyzer AI
    participant G as Policy and checks
    participant K as Fetcher with kubectl
    loop Until it can name the cause
        AI->>G: Ask for a read-only tool
        alt Not allowed
            G-->>AI: DENIED with the reason
        else Allowed
            G->>K: Run it, no shell
            K-->>AI: Evidence with secrets masked
        end
    end
    AI->>AI: Conclude the root cause
```

- **It chooses its own next step.** The AI picks which of 10 read-only tools to call, reads what comes back, and asks for more until it can name the cause (multi-turn function calling).
- **Four agents, one loop.** Producer, Fetcher, Analyzer and GitOps Scribe pass typed messages, and a state machine keeps the investigation in order.
- **Autonomy with a leash.** Caps on tool calls (12), follow-up rounds (10), tokens and model calls, plus a circuit breaker after repeated failures.
- **Guardrails in code, not hopes.** Every request passes a policy check before it runs: right namespace only, no ConfigMap reading by default, no repeated calls.
- **Tested like software.** 8 prompt-injection attack cases, all defended; a fake "obedient" AI that proves the code blocks forbidden requests; offline replay in CI.
- **A human decides.** Fixes are written as text suggestions. The approver denies every action by default.

## Quick start

Requires Go 1.24 or newer and `git`. Live mode also needs `kubectl` access and a `GEMINI_API_KEY`.

**1. Analyze a log file** (no cluster, no API key):

```bash
go run ./cmd/analyzer/main.go --log-file=/path/to/app.log
```

Secrets and email addresses are masked before anything is saved. If a model API key is set, the (masked) log text is also sent to that model service, unless you add `--llm heuristic`. With no key, nothing leaves your machine. If the model service fails, the report still contains the rule-based part, says the model part is missing, and the command exits with an error.

The report is saved in `~/log-analyzer-incidents/.log-analyzer/incidents/`, its own small Git repo. Change the place with `--git-repo`.

**2. Investigate a live cluster:**

```bash
export GEMINI_API_KEY="your-key"     # or keep it in a file: --gemini-key-file=/path/to/key (safer, see docs/CONFIG.md)
go run ./cmd/analyzer/main.go --namespace=production --service=payment-service \
  --symptom="HTTP 504 and pods crashing" --since=2h
```

Add `--dry-run` to print the `kubectl` commands instead of running them.

**3. Run it inside Kubernetes:**

```bash
make docker-build            # push the image to a registry your cluster can reach
kubectl create secret generic gemini-key --from-literal=GEMINI_API_KEY="your-key"
helm install inv1 deploy/helm/log-analyzer --set gemini.existingSecret=gemini-key \
  --set llm.mode=live --set llm.acknowledgeDataLeavesCluster=true \
  --set investigation.namespace=production --set investigation.service=payment-service
kubectl logs -f job/inv1
```

In live mode the chart mounts your Secret as a read-only file, not as an environment variable. The chart's default is `llm.mode=heuristic`: nothing leaves the cluster, but it is less accurate. Live mode sends masked log text to Gemini and needs the `acknowledgeDataLeavesCluster` switch. See [docs/DATA-FLOW.md](docs/DATA-FLOW.md).

The chart grants read-only access to the investigated namespace only (`investigation.namespace`, which must already exist), and no ConfigMap access by default. Set `rbac.clusterWide=true` to allow reading every namespace; the node tool (`get_node_conditions`) needs that.

**Try it free on a throwaway local cluster** (needs Docker and k3d; no API key, it uses the built-in rule-based model via `--llm heuristic`):

```bash
make k3d-e2e     # 3 broken apps; checks each report names the right cause
make helm-k3d    # builds the image, installs the Helm chart, checks the result
```

Both create their own cluster and delete it afterwards. To run the chart with real Gemini instead (uses a little of your free quota): `GEMINI_API_KEY=... make helm-k3d-live`.

**Which AI model?** You choose; nothing is tied to one company:

```bash
# Gemini (default)
go run ./cmd/analyzer --namespace=production --gemini-key-file="$HOME/.keys/gemini"
# Any OpenAI-compatible service: OpenAI, Groq, OpenRouter, Mistral, or Ollama on your own machine
go run ./cmd/analyzer --namespace=production --provider=openai --base-url=https://api.groq.com/openai/v1 --model=<model name> --api-key-file="$HOME/.keys/groq"
# Claude
go run ./cmd/analyzer --namespace=production --provider=anthropic --model=<model name> --api-key-file="$HOME/.keys/anthropic"
```

Model names change often, so only Gemini has a built-in default. Base addresses must use https (or localhost); see [docs/CONFIG.md](docs/CONFIG.md).
Status: Gemini and Groq (model `openai/gpt-oss-120b`) are tested against the real services; see [accuracy/RESULTS.md](accuracy/RESULTS.md). The Anthropic client is so far tested only against a local fake server.

**How accurate is it?** `make accuracy` runs small broken apps with known causes through the rule-based model and through each model service you list (`ACC_MODELS`, see the top of `scripts/accuracy.sh`), then writes the scorecard to [accuracy/RESULTS.md](accuracy/RESULTS.md), including what it does not prove. It needs the same setup as above; add `GEMINI_API_KEY` to include Gemini. The cases are split into a **dev** set (used while tuning) and a **held-out** set (never tuned against); the scorecard reports them separately. A case only counts if its fault really showed up in the cluster, and a right answer must also name the evidence. To run only some cases, one set, or to repeat each one and see how stable the answers are, set `ACC_CASES`, `ACC_SET` and `ACC_REPEAT` (also described at the top of `scripts/accuracy.sh`). `make validate-cases` checks the test apps themselves, without any model or key. How the cases work: [accuracy/README.md](accuracy/README.md).

If the rule-based model has nothing to go on, it answers `CANNOT_DETERMINE` ("cannot tell"). That is not the same as "no fault".

## What is in each report

A file named `YYYY-MM-DD-<severity>-<slug>.md` with the severity, root cause, evidence and recommendations, committed as `incident(P1): <title>`. Add `--branch` to commit on a separate branch.

The AI puts each cause into one category from a fixed list (the list is in `internal/llm/model.go`), for example out of memory, bad settings, image pull failure, failing health check, database lock or no fault. If it cannot tell, it says so (`CANNOT_DETERMINE`) instead of guessing. If the model forgets to name a category, it is asked once; if it still gives none, the report says `CANNOT_DETERMINE`.

## Safety layers

| Layer | What it does |
|---|---|
| Untrusted-data delimiters | The AI is told log text is data, never orders |
| Fixed tools | 10 read-only tools; a test fails if a write tool is added |
| Parameter checks | Patterns, length limits, no shell metacharacters |
| Policy | Right namespace, ConfigMaps not readable by default, at most 12 tool calls, no repeats |
| Redaction | Secrets are masked before anything is saved |
| Cost limits | Token and call limits, and a circuit breaker |
| No write path | An approver that denies everything is the default |

More: [docs/SECURITY.md](docs/SECURITY.md)

## Built with

Go, any of: Gemini, an OpenAI-compatible service (OpenAI, Groq, OpenRouter, Mistral, Ollama, ...) or Anthropic (all with tool calling), kubectl, Git, Docker, Helm, GitHub Actions.

## Configuration

| Setting | Meaning |
|---|---|
| `--provider`, `--model`, `--base-url` | Which model service (gemini, openai, anthropic). See [docs/CONFIG.md](docs/CONFIG.md) |
| `--api-key-file` / `LLM_API_KEY_FILE` / `LLM_API_KEY` | The key, from a file (best) or the environment, never in code. Gemini also accepts `--gemini-key-file`, `GEMINI_API_KEY_FILE`, `GEMINI_API_KEY` |
| `GEMINI_MODEL` or `--model` | Model name; the default is in `internal/llm/defaults.go` |
| `LOG_ANALYZER_MAX_CALLS`, `LOG_ANALYZER_MAX_TOKENS`, `LOG_ANALYZER_MAX_TOTAL_TOKENS` | Cost limits; 0 turns one off |

All settings: [docs/CONFIG.md](docs/CONFIG.md)

## Develop

```bash
make ci           # the full check, same as GitHub; free, needs no key
make hooks        # run it before every git push
make check-model  # can my key use this model?
make eval-live    # re-run the attack tests on the current model (costs money)
```

## Project layout

```
cmd/analyzer      the program
cmd/eval          attack tests against recorded or live AI answers
internal/agent    producer, fetcher, analyzer, git agent
internal/toolbox  the 10 read-only tools, validation, policy
internal/redact   secret masking
internal/llm      model clients (Gemini, OpenAI-compatible, Anthropic), recorder, cost limits
internal/protocol messages between agents
internal/ttlmap   map whose idle entries expire
evals/            attack cases and results
deploy/helm       Helm chart
docs/             SECURITY.md, CONFIG.md, ROADMAP.md
testdata/         sample logs
incidents/        reports written by early runs
```

## Limits and roadmap

- Early stage: tested on 8 hand-written attack cases, one model, one run. The model resisted them even without the code guards, so the guards are insurance, proven by deterministic tests.
- The AI can be wrong. A human should review every report before acting.
- Masking is pattern-based (secrets and email addresses). Unknown secret formats and other personal data can slip through.
- In live mode, logs are sent to the model service you chose and leave your environment (unless it runs on your machine). Free tiers of some services may be read or used for training; see [docs/DATA-FLOW.md](docs/DATA-FLOW.md) before using real data.
- Data in the same namespace stays readable. Run with a read-only Kubernetes account.
- It suggests fixes and never applies them.
- Reports are stored outside the repo and are not backed up unless you push that folder.
- Model names expire. Keep `--model` current; free tiers have low limits.
- The prompt-injection tests (8 attacks) have only been run on Gemini. Other models have not been tested against them yet.
- The Helm chart and Docker image passed tests on a local k3d cluster, with the rule-based model and with live Gemini. They have not run on a real production cluster.
- Not enterprise-ready yet: see [docs/ROADMAP.md](docs/ROADMAP.md).
