# Accuracy

Run on 2026-11-15 at commit `66d7680`. Each case is a small broken app on a throwaway k3d cluster with a known cause.
A case is right only if the answer's category equals the known cause. `ERROR`/`NONE` mean the run failed, and count as wrong.

| App | Known cause | Rule-based model | Gemini (`built-in default`) |
|---|---|---|---|
| config-parse | CRASHLOOP_BAD_CONFIG | ❌ CANNOT_DETERMINE | ✅ CRASHLOOP_BAD_CONFIG |
| config | CRASHLOOP_BAD_CONFIG | ✅ CRASHLOOP_BAD_CONFIG | ✅ CRASHLOOP_BAD_CONFIG |
| dblock (simulated) | DB_LOCK | ✅ DB_LOCK | ✅ DB_LOCK |
| healthy | NO_FAULT | ❌ CANNOT_DETERMINE | ✅ NO_FAULT |
| image | IMAGE_PULL_FAIL | ✅ IMAGE_PULL_FAIL | ✅ IMAGE_PULL_FAIL |
| oom | OOM_KILLED | ✅ OOM_KILLED | ✅ OOM_KILLED |
| readiness | READINESS_PROBE_FAIL | ✅ READINESS_PROBE_FAIL | ✅ READINESS_PROBE_FAIL |

**Rule-based: 5 of 7.** **Gemini: 7 of 7.**

## What this does not show
- 7 cases is a small sample, not a benchmark. One run each; AI answers can differ between runs.
- The cases are simple: one broken app alone in its namespace. Real incidents are messier.
- `dblock` is simulated: the app only prints database-lock errors, there is no real database.
- The cases were written by the author of the tool.
