# Accuracy

Run on 2026-11-17 at commit `7a04a1f`. Each case is a small broken app on a throwaway k3d cluster with a known cause.
A case is right only if the answer's category equals the known cause. `ERROR`/`NONE` mean the run failed, and count as wrong.

| App | Known cause | Rule-based model | gemini (`gemini`) |
|---|---|---|---|
| config-parse | CRASHLOOP_BAD_CONFIG | ❌ CANNOT_DETERMINE | ✅ CRASHLOOP_BAD_CONFIG |
| config | CRASHLOOP_BAD_CONFIG | ✅ CRASHLOOP_BAD_CONFIG | ✅ CRASHLOOP_BAD_CONFIG |
| dblock (simulated) | DB_LOCK | ✅ DB_LOCK | ✅ DB_LOCK |
| healthy | NO_FAULT | ❌ CANNOT_DETERMINE | ❌ NONE |
| image | IMAGE_PULL_FAIL | ✅ IMAGE_PULL_FAIL | ✅ IMAGE_PULL_FAIL |
| oom | OOM_KILLED | ✅ OOM_KILLED | ✅ OOM_KILLED |
| readiness | READINESS_PROBE_FAIL | ✅ READINESS_PROBE_FAIL | ✅ READINESS_PROBE_FAIL |

**Rule-based: 5 of 7.** **gemini: 6 of 7.**

## What this does not show
- 7 cases is a small sample, not a benchmark. One run each. Results can differ between runs: AI answers vary, and the rule-based model depends on what the cluster looks like at the moment it is checked.
- The cases are simple: one broken app alone in its namespace. Real incidents are messier.
- `dblock` is simulated: the app only prints database-lock errors, there is no real database.
- `ERROR` can also mean the model service rate-limited the run or it ran out of time. That says nothing about how accurate the model is.
- The cases were written by the author of the tool.
