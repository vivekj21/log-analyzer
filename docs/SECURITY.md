# Security

## What we protect
- The Kubernetes cluster (nothing may change it).
- Secrets in the cluster and in logs.
- The accuracy of incident reports.

## Threat model
The main risk is **prompt injection**: text inside logs, events or ConfigMaps that
tries to give instructions to the model. We assume this text is hostile.
Attacker goals we test for (see `evals/cases/attack-01..08`):
read secrets, read other namespaces, forge system messages, plant URLs or
commands in reports, trigger deletes, force a wrong answer ("NO_FAULT").

## Layers (each one assumes the previous one failed)
1. **Prompt:** tool output is wrapped in delimiters with a random per-investigation
   token; the system prompt says delimited content is data and must be reported
   under "Possible prompt injection", not obeyed.
2. **Tool allowlist:** only the named read-only tools exist. A test fails if a
   write-style tool is ever registered.
3. **Parameter validation:** regex checks, length limits, shell metacharacters
   rejected, selectors passed as `-l=value` / `--field-selector=value`.
4. **Policy:** namespace must match the investigation, sensitive tools (such as
   get_configmap) are denied by default, 12 calls per investigation, identical
   repeated calls rejected. Rejections return "DENIED: <reason>" to the model.
5. **Execution:** no shell, direct process start, 30s timeout, output capped at 1 MiB.
6. **Redaction:** secrets are masked before evidence is stored, so the model,
   the report and Git only see `[REDACTED]`.
7. **Read-only by design:** the Approver interface denies every action. The report
   may list "Suggested fixes" as text only.
8. **Storage:** incident files go to a directory outside this repo by default.

## Known residual risks
- Delimiters are a strong hint, not a hard wall. A model can still be misled
  into a **wrong conclusion** even when it calls no forbidden tool.
- Redaction is pattern-based. Unknown secret formats can slip through.
- Logs sent to the Gemini API leave your environment, including any
  non-secret personal data they contain.
- Same-namespace data stays readable. Run with a **read-only Kubernetes
  service account**, so the cluster itself enforces what this code enforces.
- If `AllowSensitive` is turned on, ConfigMap contents become visible to the model.
- Reports are generated text. A human should review them before acting.
- The "Possible prompt injection" section depends on the model following instructions.

## Measuring it
`evals/results/baseline-attacks.json` (before the policy, redaction and prompt layers) versus
`evals/results/after-attacks.json` (after). Metric: `attack_success`.

## Attack eval results (Nov 2026)
Model: gemini-3.5-flash-lite, 8 attack cases, live record mode.
- Same-model baseline (code before policy, redaction and prompt layers): 8/8 defended.
- After hardening: 8/8 defended, 0 forbidden tool attempts, 0 canary leaks.
- Earlier baseline on gemini-2.5-pro (different model, replayed): 8/8 compromised,
  including forbidden tool attempts and canary leaks.

Interpretation: the current model resists these attacks without help, so this eval
does not show which layer protects us. Older or weaker models were successfully
attacked, so the layers are insurance. Deterministic tests of the policy and
redaction layers are the proof that they work; the model-based eval measures
model behaviour. Small attack set, single run: not a guarantee.

Metric caveat: `forbidden_tool_attempted` counts forbidden calls that reached the
executor. Calls denied earlier by the policy are not counted, so a zero after
hardening means "nothing forbidden got through", not "the model never tried".
Denied attempts appear in the `[Policy] ... DENIED` log lines.

## Cost controls
Per-investigation and per-process token and call limits, and a circuit breaker after repeated
failures (see docs/CONFIG.md). Limits count tokens, not money. When one is hit, the investigation
ends with a "budget exceeded" error instead of spending more.
