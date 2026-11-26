# Known limits

Log Analyzer is an early release (v0.1.0). This page lists what you should know before relying on it. The short version is in the README, under "Good to know".

## How much to trust a report
- **The AI can be wrong.** A person should review every report before acting on it. The scorecard in [accuracy/RESULTS.md](../accuracy/RESULTS.md) shows where it is right and where it is not.
- **The safety tests are small.** There are 8 hand-written prompt-injection cases, run on one model and one run. The model resisted them even without the code guards, so the guards are insurance, proven by deterministic tests.
- **Only Gemini has been attack-tested.** The OpenAI-compatible and Anthropic providers have not been run against those 8 attacks yet.

## Your data
- **Masking is pattern-based.** It covers secrets and email addresses. Unknown secret formats and other personal data can slip through. See [DATA-FLOW.md](DATA-FLOW.md).
- **In live mode, logs are sent to the model service you chose** and leave your environment, unless it runs on your own machine. Free tiers of some services may be read or used for training. Read [DATA-FLOW.md](DATA-FLOW.md) before using real data.
- **Reports are stored outside the repository** and are not backed up unless you push that folder.

## Your cluster
- **Data in the same namespace stays readable.** Run it with a read-only Kubernetes account (the Helm chart sets this up for you).
- **It suggests fixes and never applies them.** This is by design.
- **Not yet run on a production cluster.** The Helm chart and the Docker image passed tests on a local k3d cluster, with the rule-based model and with live Gemini.

## Running it
- **Model names expire.** Keep `--model` current. Free tiers have low limits.
- **Enterprise features are planned, not built:** an audit trail, access control, an approval workflow, alert triggers and shared report storage. See [ROADMAP.md](ROADMAP.md).
