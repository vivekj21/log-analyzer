# Configuration

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `--provider` | flag / `LLM_PROVIDER` | `gemini` | `gemini`, `openai` (any OpenAI-compatible server) or `anthropic`. |
| `--model` | flag / `LLM_MODEL` | Gemini only: built-in | Model name. Required for `openai` and `anthropic`. |
| `--base-url` | flag / `LLM_BASE_URL` | the service's own | Server address for `openai`/`anthropic`. Must be https, or localhost. |
| `--allow-http` | flag / `LLM_ALLOW_HTTP=true` | off | Allows plain http to another machine (for example a model server inside your cluster). Key and logs are then not encrypted in transit. |
| `--api-key-file` | flag | none | Read the API key from this file. Best choice: see "Where the key comes from". |
| `LLM_API_KEY_FILE` | environment | none | Same as the flag, as a variable (the Helm chart uses this). |
| `LLM_API_KEY` | environment | none | The key as plain text, for any provider. |
| `OPENAI_API_KEY`, `ANTHROPIC_API_KEY` | environment | none | Used only by their own provider. |
| `--gemini-key-file` | flag | none | Older name for `--api-key-file`; only with `--provider=gemini`. |
| `GEMINI_API_KEY_FILE` | environment | none | Older name for `LLM_API_KEY_FILE`; only with `--provider=gemini`. |
| `GEMINI_API_KEY` | environment | none | Gemini key as plain text. Simple, but easier to leak than a file. Never put it in code. |
| `GEMINI_MODEL` | environment | see `internal/llm/defaults.go` | Model name. `--model` overrides it. |
| `--output`, `-o` | flag | none | Also save the report as a markdown file. An existing folder, or a value ending in `/`, gets a new named file; any other value is the file to write (replaced if it exists). The report is always shown on the screen too. |
| `--git` | flag | off | Also commit the report to a Git repository. Needs `git` on your PATH. `--git=false` always wins. |
| `--git-repo` | flag | `~/log-analyzer-incidents` (with `--git`) | The Git repository for the commit. Giving this flag turns `--git` on. |
| `--branch` | flag | off | Commit on a new branch for each incident instead of the current branch. |
| `LOG_ANALYZER_MAX_CALLS` | environment | 30 | Model calls per investigation. |
| `LOG_ANALYZER_MAX_TOKENS` | environment | 400000 | Tokens per investigation. |
| `LOG_ANALYZER_MAX_TOTAL_TOKENS` | environment | 4000000 | Tokens per process. |
| `LOG_ANALYZER_BREAKER_FAILURES` | environment | 5 | Failed calls in a row before pausing. |
| `LOG_ANALYZER_BREAKER_COOLDOWN_SECONDS` | environment | 60 | Length of the pause. |

## Where the report goes
The report is always shown on the screen (standard output). Messages and warnings go to standard error, so `analyzer ... > report.md` holds only the report.

Saving is optional: `--output` writes a markdown file, and `--git` or `--git-repo` commits to Git. If either fails (no `git` installed, a folder that cannot be written), one warning is printed, the report on the screen stays complete, and the warning does not change the exit code.

## Where the key comes from
Checked in this order; the first one that has a key wins:
1. `--api-key-file`
2. `--gemini-key-file` (Gemini only)
3. `LLM_API_KEY_FILE`
4. `GEMINI_API_KEY_FILE` (Gemini only)
5. `LLM_API_KEY`
6. the provider's own variable: `GEMINI_API_KEY`, `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`

The Gemini-named sources are skipped for other providers, so a Gemini key can never be sent to another service by mistake.

If a key file is named but is missing, empty, or holds more than just the key, the program stops with a clear message. It does **not** quietly use another source, because that could be a different key. A key file readable by all users gets a warning (use mode 0400 or 0440). The key is never printed or logged. `kubectl`, which the tool starts as a child process, does not receive any `GEMINI_*`, `LLM_*`, `OPENAI_*` or `ANTHROPIC_*` variable.

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
