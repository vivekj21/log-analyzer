# Roadmap

## Done
Read-only tool allowlist, request policy, secret redaction, untrusted-data delimiters, deny-all approver,
attack evals with a fair same-model baseline, configurable model, expiring working state, cost limits,
CI that runs the same locally and on GitHub, Docker image and Helm chart (the last two not yet tested on a real cluster).

## Next: make the core solid
1. Release working state when an investigation ends, and check the policy and producer for races.
2. Real-incident test set: first version done (7 labelled apps, `make accuracy`, scorecard in accuracy/RESULTS.md). Next: more and messier cases, including real past incidents.

## Then: decide before building
3. Where logs go and credentials: done. Decisions in docs/DATA-FLOW.md; the key comes from a file or the environment through a small provider interface (internal/creds), the chart mounts it as a file. Still open: stronger masking of personal data beyond emails; direct secret-manager providers if people ask.
4. Credentials: a plugin mechanism plus a good built-in one.

## Later: after the core is solid
Investigation record (an append-only, redacted audit trail with a retention period), observability, access control,
approval workflow, triggers and outputs (alerts in, Slack or tickets out), shared report storage, stronger masking,
vulnerability scanning.
