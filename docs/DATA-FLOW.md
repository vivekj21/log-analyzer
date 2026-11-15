# Where your data goes

Short version: **in the default mode nothing leaves your cluster. In live mode, log text is sent to Google's Gemini API.**

## Pick a mode

| Mode | Where the data goes | Accuracy (7 test apps) |
|---|---|---|
| Rule-based (`--llm heuristic`, the Helm default) | Nowhere. Everything stays on the machine or in the cluster. | 5 of 7. When unsure it says "cannot tell". |
| Live (`--llm live`) | Masked log text goes to the Gemini API. | 7 of 7 |

The scorecard is in [accuracy/RESULTS.md](../accuracy/RESULTS.md). It is a small test, so read its limits before trusting the numbers.
The safe default is less accurate on purpose. You switch to live mode yourself, knowing what leaves.

## What is sent to Gemini in live mode
- **Cluster investigation:** the symptom text you typed, and the output of the read-only kubectl tools the AI asks for (for example pod lists, events, pod logs, deployment specs). Names of namespaces, pods and services appear in that text.
- **Log-file mode:** if `GEMINI_API_KEY` is set, the first 50,000 characters of the file. With no key, nothing is sent.
- The AI's answers come back and are used to write the report.

## What is masked first
Before tool output or a log file is sent, stored or committed, these are replaced with `[REDACTED]`:
passwords, tokens, API keys and similar `name=value` pairs, `Authorization` headers, AWS keys, private-key blocks, Kubernetes Secret values, and **email addresses**.

## What is NOT masked
Masking is pattern-based, so it is a safety net, not a guarantee.
- Anything you type in `--symptom` (do not put secrets there).
- Names, phone numbers, IP addresses, customer or order IDs, and any secret in a format the patterns do not know.
- Free text in your logs that does not look like a secret or an email.

If your logs can contain personal data of that kind, use the rule-based mode or do not send them.

## What is stored
The incident report is committed to a Git repo (`--git-repo`; in the Helm chart `/data/incidents`, which is lost when the pod is deleted unless `storage.persistence=true`). It holds the symptom, the AI's conclusion, and up to 10,000 characters of masked evidence. Record mode (used for tests) also saves the model conversation to local files; treat those as sensitive.

## What never leaves
The Gemini API key is read from an environment variable and used only to sign in to the API. Cluster access uses the pod's Kubernetes service account, with read-only permissions, and those credentials are not part of what is sent.

## Gemini free tier versus paid
Google's Gemini API terms treat these differently. In the version of the terms I checked, unpaid use lets human reviewers read inputs and outputs to improve Google's products, and tells you not to submit sensitive, confidential or personal information. Paid use (a project with billing enabled) says prompts and responses are not used to improve products.
So: the free tier is fine for demos and fake data. For real logs use a paid project, and re-read Google's current terms first, because they change.

## Helm chart safeguard
The chart defaults to `llm.mode=heuristic`. `llm.mode=live` refuses to install until you also pass `--set llm.acknowledgeDataLeavesCluster=true`. `make ci` checks this.

---
*This page describes how the code behaves. It is not legal advice. Check your own privacy and legal requirements and Google's current terms before sending real data to any external service.*
