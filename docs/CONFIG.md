# Configuration

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `GEMINI_API_KEY` | environment | none | Gemini key. Never put it in code. |
| `GEMINI_MODEL` | environment | see `internal/llm/defaults.go` | Model name. `--model` overrides it. |
| `LOG_ANALYZER_MAX_CALLS` | environment | 30 | Model calls per investigation. |
| `LOG_ANALYZER_MAX_TOKENS` | environment | 400000 | Tokens per investigation. |
| `LOG_ANALYZER_MAX_TOTAL_TOKENS` | environment | 4000000 | Tokens per process. |
| `LOG_ANALYZER_BREAKER_FAILURES` | environment | 5 | Failed calls in a row before pausing. |
| `LOG_ANALYZER_BREAKER_COOLDOWN_SECONDS` | environment | 60 | Length of the pause. |

A value of 0 turns a limit off. Idle per-investigation state is dropped after 2 hours.

## Commands
- `make ci`: the full check, same as GitHub. Free, needs no key.
- `make check-model`: can my key use this model?
- `make eval-live`: re-record the attack tests on the current model (costs money).
- `make hooks`: run `make ci` before every `git push`.
- `make docker-build`, `helm install ...`: see `deploy/helm/log-analyzer`.
