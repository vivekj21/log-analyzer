# Configuration

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `--gemini-key-file` | flag | none | Read the Gemini key from this file. Best choice: see "Where the key comes from". |
| `GEMINI_API_KEY_FILE` | environment | none | Same as the flag, as a variable (the Helm chart uses this). |
| `GEMINI_API_KEY` | environment | none | Gemini key as plain text. Simple, but easier to leak than a file. Never put it in code. |
| `GEMINI_MODEL` | environment | see `internal/llm/defaults.go` | Model name. `--model` overrides it. |
| `LOG_ANALYZER_MAX_CALLS` | environment | 30 | Model calls per investigation. |
| `LOG_ANALYZER_MAX_TOKENS` | environment | 400000 | Tokens per investigation. |
| `LOG_ANALYZER_MAX_TOTAL_TOKENS` | environment | 4000000 | Tokens per process. |
| `LOG_ANALYZER_BREAKER_FAILURES` | environment | 5 | Failed calls in a row before pausing. |
| `LOG_ANALYZER_BREAKER_COOLDOWN_SECONDS` | environment | 60 | Length of the pause. |

## Where the key comes from
Checked in this order; the first one that has a key wins:
1. `--gemini-key-file`
2. `GEMINI_API_KEY_FILE`
3. `GEMINI_API_KEY`

If a key file is named but is missing, empty, or holds more than just the key, the program stops with a clear message. It does **not** quietly use another source, because that could be a different key. A key file readable by all users gets a warning (use mode 0400 or 0440). The key is never printed or logged. `kubectl`, which the tool starts as a child process, does not receive any `GEMINI_*` variable.

A file works with any secret tool, because they can all write one:
- **Kubernetes Secret:** mount it as a volume. The Helm chart does exactly this in live mode (read-only, mode 0440).
- **HashiCorp Vault / OpenBao:** Vault Agent can render a secret to a file in the pod.
- **Google, AWS or Azure secret managers:** the Secrets Store CSI driver mounts them as files, or External Secrets Operator copies them into a Kubernetes Secret.
- **SOPS:** decrypt to a file at deploy time (`sops -d`).

A new source (for example a cloud secret manager called directly) is one small Go type that implements `creds.Provider`; see `internal/creds`.

A value of 0 turns a limit off. Idle per-investigation state is dropped after 2 hours.

## Commands
- `make ci`: the full check, same as GitHub. Free, needs no key.
- `make check-model`: can my key use this model?
- `make eval-live`: re-record the attack tests on the current model (costs money).
- `make hooks`: run `make ci` before every `git push`.
- `make docker-build`, `helm install ...`: see `deploy/helm/log-analyzer`.
