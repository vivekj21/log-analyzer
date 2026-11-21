# Accuracy cases

Each file in `accuracy/cases/` is one small broken app with a known cause. `make accuracy` starts them on a throwaway k3d cluster, asks the analyzer what is wrong, and compares the answer with the known cause. The scorecard is `RESULTS.md`.

## What the analyzer may answer (one category)

The list lives in `internal/llm/model.go`. `make ci` fails if the Go code, the prompt and this file disagree.

| Category | Meaning |
|---|---|
| `OOM_KILLED` | A container used more memory than its limit and was killed. |
| `CRASHLOOP_BAD_CONFIG` | The app starts, then exits because of wrong settings, a wrong command, or an invalid file. |
| `IMAGE_PULL_FAIL` | The image cannot be pulled (wrong name or tag, registry not reachable, no permission). |
| `READINESS_PROBE_FAIL` | The container runs, but its readiness probe fails, so it gets no traffic. |
| `LIVENESS_PROBE_KILL` | A liveness probe fails, so Kubernetes keeps killing and restarting the container. |
| `DB_LOCK` | Database queries wait on, or time out on, locks. |
| `DEPENDENCY_DOWN` | A service the app needs has no running pods or refuses connections, and that broke first. |
| `DNS_FAIL` | A name lookup fails (wrong service name, wrong namespace in the name, DNS down). |
| `QUOTA_EXCEEDED` | A ResourceQuota or LimitRange stops pods from being created. |
| `RBAC_DENIED` | The Kubernetes API answers "forbidden" to the app's service account. |
| `UNSCHEDULABLE` | A pod stays Pending because no node fits it. |
| `STORAGE_FAIL` | A volume claim cannot be bound or mounted. |
| `SERVICE_MISROUTE` | A Service does not send traffic to healthy pods (selector or port does not match). |
| `MISSING_CONFIG_OBJECT` | A pod cannot start because a ConfigMap or Secret it references does not exist. |
| `NO_FAULT` | Nothing is wrong. A normal, correct answer. |
| `CANNOT_DETERMINE` | The evidence does not show the cause. Never means "healthy". |

## The two sets

- **dev**: used while writing and tuning the prompt and the code.
- **held-out**: never used for tuning. If a result on a held-out case is bad, that is a finding to report, not a case to fix the prompt for. Look at the scorecard's held-out numbers first.

Held-out cases are written by the same author as the dev cases, so they are not fully independent. Real past incidents are the next step.

## A case file

Comment lines at the top, then ordinary Kubernetes YAML. Every object lives in the namespace `la-acc-<case name>` (or one that starts with it). Cluster-wide objects are not allowed. Every pod carries the label `app: <name>`.

| Line | Meaning |
|---|---|
| `# expect:` | The known cause (a category). If two things are broken, the one behind the reported symptom. |
| `# symptom:` | What the user says. It describes the problem, not the cause. |
| `# set:` | `dev` or `heldout`. |
| `# service:` | Optional. The service name given to the analyzer (default: the case name). `-` gives none, for cases with several apps. |
| `# verify:` | A shell condition that is true once the fault is really visible. Helpers: `logs_have`, `reasons_have`, `has_event`, `phase`, `restarts` (see `scripts/case-lib.sh`). |
| `# must-mention:` | A regular expression the report must match to count as explained. Repeatable. |
| `# must-not-claim:` | A regular expression the report must not match (a false claim). Repeatable. |
| `# simulated:` | Optional. What is printed text instead of real behaviour. It is listed on the scorecard. |
| `# note:` | Optional. Why the case exists. |

## How a case is scored

1. **Is the fault visible?** After the wait, `# verify:` is checked. A case whose fault did not show up is **not scored** and listed as such. A broken test app never counts against a model.
2. **Category.** Right only if the answer's category equals `# expect:`. `CANNOT_DETERMINE` is "unsure": not right, not a wrong diagnosis.
3. **Explanation.** For right answers only, the report must match every `# must-mention:` and no `# must-not-claim:`. This is a keyword check: it catches a report that never names the evidence.

## Failed runs and kept answers

- A failed run (⛔) is **not** a wrong answer. The scorecard says why: `rate_limit` (limit or quota reached), `network` (service not reachable; retried twice, after 20 s and 60 s, before it counts), `auth` (key refused), `timeout`, or `other`. After 3 failed runs in a row a model is skipped for the rest of the run, and the skipped runs say what the last reason was.
- Answers are kept per commit, case, model and run number (in `~/log-analyzer-accuracy-logs/cells`). A later run at the same commit reuses them and only asks for what is missing. So a partial run is not wasted: run `ACC_SET=heldout make accuracy`, then `ACC_SET=dev make accuracy`, then `make accuracy`, and the last one only fills the gaps and writes the full `RESULTS.md`. Failed runs are never kept, and any new commit (or uncommitted change) starts fresh. `ACC_REUSE=0` turns this off.
- Only a full run, with every case, is written to `accuracy/RESULTS.md`. A partial run is printed and saved in its log folder, so the committed scorecard always compares like with like.

## Commands

- `make validate-cases`: starts every case and checks that its fault shows up. No key, no model, no cost. Run it after adding or changing a case.
- `make accuracy`: the full scorecard. Options are at the top of `scripts/accuracy.sh` (`ACC_SET=heldout`, `ACC_CASES="oom image"`, `ACC_REPEAT=3`).
- `make check-cases`: static checks, part of `make ci`.

## Known gaps

- The analyzer's tools cannot read NetworkPolicies, Roles or ResourceQuotas directly. Cases that depend on them are answered from events and app logs. A network-policy case is left out until a tool can show policies.
- CPU throttling and slow latency need metrics, which the cases do not provide yet.
- Only a few categories have more than two cases, so one wrong answer moves their score a lot.
