# Autonomous Multi-Agent Kubernetes & Log Root-Cause Deductor in Go

An enterprise-grade, autonomous multi-agent log analysis, root-cause deduction, and GitOps incident tracking system written in Go.

---

## 4-Agent Autonomous Architecture

```
                      ┌─────────────────────────────────────────┐
                      │             Producer Agent              │
                      │  - Investigation State Machine (Init,   │
                      │    Gathering, Analysis, Follow-up, Done)│
                      │  - Concurrency & Cycle Orchestration    │
                      └──────────────┬────────────┬─────────────┘
                                     │            │
            Dispatches Tool Calls    │            │ Dispatches Evidence Context
           & Follow-up Iterations    │            │ & Tool Call Returns
                                     ▼            ▼
   ┌───────────────────────────────────┐        ┌──────────────────────────────────┐
   │         Log Fetcher Agent         │        │        Log Analyzer Agent        │
   │  - Strict Hardcoded Tool Allowlist│        │  - Gemini Long-Context Reasoning │
   │  - Parameter Pattern Sanitization │        │  - Multi-Turn Function Calling   │
   │  - Non-shell Direct Subprocess    │        │  - Failure Pattern Clustering    │
   │  - Cap output & enforce timeout   │        │  - SRE Recommendations Engine    │
   └───────────────────────────────────┘        └────────────────┬─────────────────┘
                                                                 │
                                                                 │ Persist Incident
                                                                 ▼
                                                ┌──────────────────────────────────┐
                                                │        GitOps Scribe Agent       │
                                                │  - Log Issue & Anomaly Extractor │
                                                │  - YAML Frontmatter + Postmortem │
                                                │  - Auto-Branch (`incident/...`)  │
                                                │  - Git Commit & Audit Trail      │
                                                └──────────────────────────────────┘
```

---

## The 4 Agents

### 1. Producer Agent (Orchestrator)
- **Role**: Coordinates the entire investigation loop and manages mutable state safely (`sync.RWMutex`).
- **State Machine**:
  - `PhaseInit` $\rightarrow$ Triggered via incident input or log stream.
  - `PhaseContextGathering` $\rightarrow$ Dispatches non-invasive discovery commands (`list_pods`, `get_events`).
  - `PhaseAnalysis` $\rightarrow$ Packages telemetry and prompts the Analyzer Agent.
  - `PhaseFollowUp` $\rightarrow$ Dynamically triggers subsequent investigative tools requested by the Analyzer.
  - `PhaseComplete` / `PhaseFailed` $\rightarrow$ Compiles the final SRE incident report and dispatches to the Git Agent.
- **Circuit Breaker**: Capped via `MaxFollowUpRounds` (default: 10) to prevent runaways and control costs.

### 2. Log Fetcher Agent (Sandboxed Security Boundary)
Enforces a **Default-Deny** security posture:
- **Hardcoded Tool Schema & Allowlist**:
  - `get_pod_logs`, `list_pods`, `describe_pod`, `get_events`, `get_deployment`, `get_service_endpoints`, `get_node_conditions`, `get_resource_usage`, `get_configmap`, `get_hpa_status`.
- **Security Guarantees**:
  - Direct kernel execution (`os/exec.CommandContext`) without shell (`/bin/sh` banned).
  - Metacharacter blacklist (`;`, `|`, `&`, `` ` ``, `$()`, `${}`, `<`, `>`, `\n`).
  - Parameter regex sanitization (RFC 1123 DNS subdomains, namespace patterns).
  - Process limits: `Stdin=nil` (no TTY hijacking), 30s timeout, stdout/stderr truncated at 1 MiB.

### 3. Log Analyzer Agent (Reasoning Layer)
- **Role**: Leverages Gemini's long-context window (up to 1M+ tokens) to reason across vast logs, stack traces, and cluster events.
- **Multi-Turn Function Calling**: When the model notices an anomaly (e.g. restart count > 0, OOMKilled flag), it dynamically calls back to the Fetcher for deeper logs.
- **Root-Cause Deductions**: Categorizes failures by signature (OOM, CrashLoop, SIGSEGV, connection timeouts, deadlocks) and outputs high-confidence remediation steps.

### 4. GitOps Scribe Agent (Issue Deductor & Git Persistence)
- **Role**: Reads logs, deduces issues, extracts error signatures, and **sticks them in Git**.
- **Issue Deduction Engine**:
  - Parses logs for OOM (exit code 137), Panics, CrashLoops, 5xx cascades, and database locks.
  - Assigns severity levels: P0 (Critical), P1 (High), P2 (Medium), P3 (Low).
- **Git Sticking & Versioning**:
  - Writes structured postmortems (`incidents/YYYY-MM-DD-<severity>-<slug>.md`) with YAML frontmatter.
  - Creates dedicated incident branches (e.g. `incident/2026-10-27-<id>`).
  - Commits the incident into Git with an audit trail and semantic message (`incident(P0): ...`).

---

## Directory Structure

```
log_analyzer/
├── go.mod
├── go.sum
├── README.md
├── cmd/
│   └── analyzer/
│       └── main.go                 # CLI entrypoint wiring all 4 agents
└── internal/
    ├── protocol/
    │   └── messages.go             # Typed message bus, envelopes & state machine
    ├── toolbox/
    │   ├── schema.go               # Hardcoded Allowlist & strict parameter validators
    │   ├── executor.go             # Sandboxed direct-subprocess runner (kubectl only)
    │   └── schema_test.go          # Unit tests verifying injection prevention & deny-default
    ├── agent/
    │   ├── producer.go             # Producer orchestrator implementation
    │   ├── fetcher.go              # Fetcher execution agent implementation
    │   ├── analyzer.go             # Analyzer reasoning agent implementation
    │   ├── git_agent.go            # GitOps Scribe: Issue deduction & Git persistence
    │   └── git_agent_test.go       # Unit tests for log parsing & Git commit operations
    └── llm/
        └── gemini.go               # Gemini SDK wrapper, multi-turn chat & tool bindings
```

---

## Running the System

### 1. Requirements
- Go 1.22+
- `git` installed
- `kubectl` configured with cluster access (or run with `--dry-run` or `--log-file`)
- `GEMINI_API_KEY` (required for LLM reasoning; optional for offline heuristic log file analysis)

### 2. Execution Modes

#### Mode A: Read Log File, Figure Out Issues, and Stick in Git
Analyze any local application log file directly without needing a live Kubernetes cluster:

```bash
go run ./cmd/analyzer/main.go \
  --log-file="/path/to/application.log" \
  --git-repo="."
```

#### Mode B: Autonomous Kubernetes Incident Investigation
Investigate a live Kubernetes namespace, deduce root cause, and commit the postmortem to Git:

```bash
export GEMINI_API_KEY="your-gemini-api-key"

go run ./cmd/analyzer/main.go \
  --namespace="production" \
  --service="payment-service" \
  --symptom="HTTP 504 Gateway Timeouts and pods crashing repeatedly" \
  --since="2h" \
  --model="gemini-2.5-pro" \
  --git=true
```

#### Mode C: Dry-Run Mode (Simulation)
Test the entire agent orchestration flow without executing live commands against Kubernetes:

```bash
export GEMINI_API_KEY="your-gemini-api-key"

go run ./cmd/analyzer/main.go \
  --namespace="staging" \
  --symptom="CrashLoopBackOff observed" \
  --dry-run=true
```

---

## Testing

Run all unit tests across all packages:

```bash
go test -v ./...
```
