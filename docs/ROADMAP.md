# Roadmap

## Done
Read-only tool allowlist, request policy, secret redaction, untrusted-data delimiters, deny-all approver,
attack evals with a fair same-model baseline, configurable model, expiring working state, cost limits,
CI that runs the same locally and on GitHub, Docker image and Helm chart (the last two not yet tested on a real cluster),
structured reports whose quoted evidence is checked against the tool output, and a REST API (`log-analyzer serve`, see API.md).

## Next: make the core solid
1. Release working state when an investigation ends, and check the policy and producer for races.
2. Real-incident test set: second version done (labelled apps with a dev and a held-out set, checked that each fault really shows up, explanation checklist; `make accuracy`, scorecard in accuracy/RESULTS.md, rules in accuracy/README.md). Next: cases taken from real past incidents, and tools that can show NetworkPolicies and metrics.

## Then: decide before building
3. Where logs go and credentials: done. Decisions in docs/DATA-FLOW.md; the key comes from a file or the environment through a small provider interface (internal/creds), the chart mounts it as a file. Model providers: Gemini, OpenAI-compatible (Groq, OpenRouter, OpenAI, Ollama ...) and Anthropic are supported; accuracy on a real OpenAI-compatible service (Groq) is measured, see accuracy/RESULTS.md; the Anthropic client has not run against the real service yet, and prompt-injection results for both new providers are still to be measured. Still open: stronger masking of personal data beyond emails; direct secret-manager providers if people ask.
4. Credentials: a plugin mechanism plus a good built-in one.

## Next: make it easy to adopt
5. A Helm Deployment and Service for `serve`, so a team can run it always on.
6. Push finished reports to Slack or a webhook, so nobody has to poll when an alert fires.

## Later: after the core is solid
Investigation record (an append-only, redacted audit trail with a retention period), observability,
approval workflow, more triggers and outputs (tickets out), shared report storage, stronger masking,
vulnerability scanning.

**Access control and audit are deliberately later.** Today the tool is read-only and safe by default: read-only tools,
masked secrets, injection defences, a read-only Kubernetes account in the chart, and a token whenever the REST API is
opened to the network. Per-user permissions, single sign-on and an audit trail are built when real users ask for them.
Ease of use comes first.
