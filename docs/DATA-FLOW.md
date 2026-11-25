# Where your data goes

Short version: **in the default mode nothing leaves your cluster. In live mode, log text is sent to the model service you chose (Gemini, an OpenAI-compatible service, or Anthropic).**

## Pick a mode

| Mode | Where the data goes | Accuracy |
|---|---|---|
| Rule-based (`--llm heuristic`, the Helm default) | Nowhere, even if a key is present. Everything stays on the machine or in the cluster. | Lower than live. See the scorecard; it varies between runs. When unsure it says "cannot tell". |
| Live (`--llm live`) | Masked log text goes to the model service you chose. If that is a server on your own machine (for example Ollama), it stays there. | See the scorecard for each model service. |

The scorecard is in [accuracy/RESULTS.md](../accuracy/RESULTS.md). It is a small test, so read its limits before trusting the numbers.
The safe default is less accurate on purpose. You switch to live mode yourself, knowing what leaves.

## What is sent to the model service in live mode
- **Cluster investigation:** the symptom text you typed, and the output of the read-only kubectl tools the AI asks for (for example pod lists, events, pod logs, deployment specs). Names of namespaces, pods and services appear in that text.
- **Log-file mode:** if a model API key is set, the first 50,000 characters of the file. With no key, or with `--llm heuristic`, nothing is sent.
- The AI's answers come back and are used to write the report.

## What is masked first
Before tool output or a log file is sent, stored or committed, these are replaced with `[REDACTED]`:
passwords, tokens, API keys and similar `name=value` pairs (also in JSON, and as environment `name:`/`value:` pairs in YAML), passwords inside URLs (`scheme://user:password@host`), `Authorization` headers, AWS keys, GitHub, Google API, Stripe, Slack and `sk-` keys, JSON Web Tokens, private-key blocks, Kubernetes Secret values, and **email addresses**.

## What is NOT masked
Masking is pattern-based, so it is a safety net, not a guarantee.
- Anything you type in `--symptom` (do not put secrets there).
- Names, phone numbers, IP addresses, customer or order IDs, a secret written inside a sentence (for example "the password is ..."), and any secret in a format the patterns do not know.
- Free text in your logs that does not look like a secret or an email.

If your logs can contain personal data of that kind, use the rule-based mode or do not send them.

## What is stored
By default the report is only shown on the screen and nothing is stored. With `--output` it is also saved as a file (the same text as on the screen). With `--git` or `--git-repo` it is also committed to a Git repo. A Git commit holds the symptom, the AI's conclusion, and up to 10,000 characters of masked evidence. In the Helm chart the Job commits to `/data/incidents`, which is lost when the pod is deleted unless `storage.persistence=true`; the report is also in the pod log. Record mode (used for tests) also saves the model conversation to local files; treat those as sensitive.

## What never leaves
The API key is read from a file (best) or an environment variable and used only to sign in to the API. It is never logged, and the `kubectl` processes the tool starts do not receive it. In the Helm chart it is mounted as a read-only file, not an environment variable. Cluster access uses the pod's Kubernetes service account, with read-only permissions, and those credentials are not part of what is sent.

## Free tiers versus paid
Each model service has its own rules on who may read your data and whether it is used for training. This changes often, so read the current terms of the service you pick before sending real logs.
What I found: Google's Gemini API terms treat free and paid use differently. In the version I checked, unpaid use lets human reviewers read inputs and outputs to improve Google's products and tells you not to submit sensitive, confidential or personal information; paid use (a project with billing enabled) says they are not used to improve products. A comparison page dated 18 October 2026 (a third party, not the providers) listed Groq and Cloudflare Workers AI as free tiers that state they do not train on API data, and Gemini (outside the EU, UK and Switzerland) and Mistral as free tiers that may.
So: free tiers are fine for demos and fake data. For real logs use a paid account whose terms you have read, or a model server you run yourself.

## Helm chart safeguard
The chart defaults to `llm.mode=heuristic`. The same switch covers every provider. `llm.mode=live` refuses to install until you also pass `--set llm.acknowledgeDataLeavesCluster=true`. `make ci` checks this.

---
*This page describes how the code behaves. It is not legal advice. Check your own privacy and legal requirements and Google's current terms before sending real data to any external service.*
