# Accuracy

Run on 2026-11-25 at commit `bafa7c6`. Each case is a small broken app on a throwaway k3d cluster with a known cause.
A run is right (✅) only if the answer's category equals the known cause. `CANNOT_DETERMINE` (❔ unsure) means the model said it could not tell: not right, but not a wrong diagnosis. Any other category is wrong (❌). `ERROR`/`NONE` (⛔) mean the run failed.
Each case was run 3 times per model. A cell shows how the runs split, then what each run answered.
| App | Set | Known cause | Rule-based model | gemini (`gemini`) |
|---|---|---|---|---|
| config-parse | dev | CRASHLOOP_BAD_CONFIG | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG |
| config | dev | CRASHLOOP_BAD_CONFIG | ✅ 3 right: CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG | ✅ 3 right: CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG |
| dblock (simulated) | dev | DB_LOCK | ✅ 3 right: DB_LOCK, DB_LOCK, DB_LOCK | ✅ 3 right: DB_LOCK, DB_LOCK, DB_LOCK |
| dependency-chain (simulated) | heldout | DEPENDENCY_DOWN | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: DEPENDENCY_DOWN, DEPENDENCY_DOWN, DEPENDENCY_DOWN |
| dns-cross-namespace | heldout | DNS_FAIL | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: DNS_FAIL, DNS_FAIL, DNS_FAIL |
| dns-wrong-name | dev | DNS_FAIL | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: DNS_FAIL, DNS_FAIL, DNS_FAIL |
| flaky-oom | heldout | OOM_KILLED | ❔ 2 right, 1 unsure: OOM_KILLED, OOM_KILLED, CANNOT_DETERMINE | ✅ 3 right: OOM_KILLED, OOM_KILLED, OOM_KILLED |
| healthy | dev | NO_FAULT | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: NO_FAULT, NO_FAULT, NO_FAULT |
| image | dev | IMAGE_PULL_FAIL | ✅ 3 right: IMAGE_PULL_FAIL, IMAGE_PULL_FAIL, IMAGE_PULL_FAIL | ✅ 3 right: IMAGE_PULL_FAIL, IMAGE_PULL_FAIL, IMAGE_PULL_FAIL |
| liveness-too-strict | dev | LIVENESS_PROBE_KILL | ❌ 3 wrong: READINESS_PROBE_FAIL, READINESS_PROBE_FAIL, READINESS_PROBE_FAIL | ✅ 3 right: LIVENESS_PROBE_KILL, LIVENESS_PROBE_KILL, LIVENESS_PROBE_KILL |
| long-noisy-log | heldout | CRASHLOOP_BAD_CONFIG | ✅ 3 right: CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG | ✅ 3 right: CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG |
| misleading-log | dev | OOM_KILLED | ✅ 3 right: OOM_KILLED, OOM_KILLED, OOM_KILLED | ✅ 3 right: OOM_KILLED, OOM_KILLED, OOM_KILLED |
| missing-configmap | dev | MISSING_CONFIG_OBJECT | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: MISSING_CONFIG_OBJECT, MISSING_CONFIG_OBJECT, MISSING_CONFIG_OBJECT |
| oom | dev | OOM_KILLED | ✅ 3 right: OOM_KILLED, OOM_KILLED, OOM_KILLED | ✅ 3 right: OOM_KILLED, OOM_KILLED, OOM_KILLED |
| postgres-bad-password | heldout | CRASHLOOP_BAD_CONFIG | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ❌ 2 right, 1 wrong: CRASHLOOP_BAD_CONFIG, DEPENDENCY_DOWN, CRASHLOOP_BAD_CONFIG |
| postgres-real-lock | dev | DB_LOCK | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: DB_LOCK, DB_LOCK, DB_LOCK |
| pvc-bad-storageclass | dev | STORAGE_FAIL | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: STORAGE_FAIL, STORAGE_FAIL, STORAGE_FAIL |
| quota-cpu | heldout | QUOTA_EXCEEDED | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: QUOTA_EXCEEDED, QUOTA_EXCEEDED, QUOTA_EXCEEDED |
| quota-pods | dev | QUOTA_EXCEEDED | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: QUOTA_EXCEEDED, QUOTA_EXCEEDED, QUOTA_EXCEEDED |
| rbac-configmap | heldout | RBAC_DENIED | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: RBAC_DENIED, RBAC_DENIED, RBAC_DENIED |
| rbac-list-pods | dev | RBAC_DENIED | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: RBAC_DENIED, RBAC_DENIED, RBAC_DENIED |
| readiness | dev | READINESS_PROBE_FAIL | ✅ 3 right: READINESS_PROBE_FAIL, READINESS_PROBE_FAIL, READINESS_PROBE_FAIL | ✅ 3 right: READINESS_PROBE_FAIL, READINESS_PROBE_FAIL, READINESS_PROBE_FAIL |
| red-herring | dev | IMAGE_PULL_FAIL | ✅ 3 right: IMAGE_PULL_FAIL, IMAGE_PULL_FAIL, IMAGE_PULL_FAIL | ✅ 3 right: IMAGE_PULL_FAIL, IMAGE_PULL_FAIL, IMAGE_PULL_FAIL |
| redis-down (simulated) | dev | DEPENDENCY_DOWN | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: DEPENDENCY_DOWN, DEPENDENCY_DOWN, DEPENDENCY_DOWN |
| scary-healthy | heldout | NO_FAULT | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: NO_FAULT, NO_FAULT, NO_FAULT |
| selector-mismatch | heldout | SERVICE_MISROUTE | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: SERVICE_MISROUTE, SERVICE_MISROUTE, SERVICE_MISROUTE |
| service-port-mismatch | dev | SERVICE_MISROUTE | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: SERVICE_MISROUTE, SERVICE_MISROUTE, SERVICE_MISROUTE |
| silent-crash | heldout | CANNOT_DETERMINE | ✅ 3 right: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ❌ 3 wrong: CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG, CRASHLOOP_BAD_CONFIG |
| two-faults | dev | OOM_KILLED | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: OOM_KILLED, OOM_KILLED, OOM_KILLED |
| unschedulable-cpu | dev | UNSCHEDULABLE | ❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE | ✅ 3 right: UNSCHEDULABLE, UNSCHEDULABLE, UNSCHEDULABLE |

**Rule-based: dev 21 of 60 runs right (36 unsure, 3 wrong); held-out 8 of 30 runs right (22 unsure).** **gemini: dev 60 of 60 runs right; held-out 26 of 30 runs right (4 wrong).**

The **dev** set is what the prompt and the code were tuned against. The **held-out** set was never used for tuning, so its numbers are the more honest ones.

## Explanations
A right category is not enough: for every right answer the report is also checked for the terms the case requires (`# must-mention`) and for false claims (`# must-not-claim`).
**Rule-based: 29 of 29 right answers also passed the checklist.** **gemini: 85 of 86 right answers also passed the checklist.**
- gemini, dependency-chain, run 2: missing: redis

## What this does not show
- 30 cases is a small sample, not a benchmark. Each case was run 3 times per model. Results can differ between runs: AI answers vary, and the rule-based model depends on what the cluster looks like at the moment it is checked.
- Simulated or partly simulated: dblock: the app only prints database-lock errors; there is no real database; dependency-chain: the frontend's 502 lines are printed text; the Redis outage itself (no endpoints behind the Service) is real; redis-down: Redis is a Deployment scaled to 0 replicas (someone scaled it down); there is no real Redis running.
- The apps are tiny stand-ins, not real systems. The Kubernetes behaviour in most cases is real (DNS, quotas, access rules, scheduling, volumes, probes, two cases use a real Postgres), but the applications only print a few lines.
- The held-out cases were not used for tuning, but the same author wrote them at the same time as the categories, so they are not independent. Real past incidents are still to be added.
- The rule-based model only knows the simplest causes, so it cannot be right on newer categories. Its column is a baseline, not a competitor.
- `ERROR` can also mean the model service rate-limited the run or it ran out of time. That says nothing about how accurate the model is. A model can also fail by calling a tool that does not exist: the analyzer retries that twice, so an `ERROR` that remains means every try failed.
- The checks on the explanation are keyword checks. They catch a report that never names the evidence, not every weak explanation.
- The cases were written by the author of the tool.
