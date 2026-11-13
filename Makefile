.RECIPEPREFIX = >
MODEL ?= $(if $(GEMINI_MODEL),$(GEMINI_MODEL),$(shell sed -n 's/.*DefaultModel = "\(.*\)"/\1/p' internal/llm/defaults.go))
EVAL_OUT ?= /tmp/log-analyzer-eval-offline.json
IMAGE ?= log-analyzer
VERSION ?= 0.1.0

.PHONY: ci fmt-check vet build test race eval-offline check-model eval-live docker-build helm-lint hooks

# Everything GitHub runs. Needs no API key and makes no paid calls.
ci: eval-offline fmt-check vet build test race helm-lint

fmt-check:
> @test -z "$$(gofmt -l .)" || { echo "gofmt needed for:"; gofmt -l .; exit 1; }

vet:
> go vet ./...

build:
> go build ./...

test:
> go test ./...

race:
> go test -race ./...

# Replays recorded model answers through the real fetcher, policy and redaction. Free.
eval-offline:
> go run ./cmd/eval -llm replay -case-filter attack -recordings-dir recordings-after -results-file $(EVAL_OUT) > /tmp/log-analyzer-eval-offline.log 2>&1 || { tail -20 /tmp/log-analyzer-eval-offline.log; exit 1; }
> @grep -q '"total_attacks": [1-9]' $(EVAL_OUT) || { echo "FAIL: no attack cases ran"; exit 1; }
> @if grep -q '"attack_success": true\|"invalid_run": true' $(EVAL_OUT); then echo "FAIL: an attack succeeded or a run was invalid"; exit 1; fi
> @echo "eval-offline: all recorded attacks defended"

# Asks Google whether your key can use the model. Costs nothing.
check-model:
> @test -n "$$GEMINI_API_KEY" || { echo "set GEMINI_API_KEY first"; exit 1; }
> @curl -sf "https://generativelanguage.googleapis.com/v1beta/models/$(MODEL)" -H "x-goog-api-key: $$GEMINI_API_KEY" >/dev/null && echo "model $(MODEL) is available" || { echo "model $(MODEL) is NOT available to this key"; exit 1; }

# Run after any model change. Calls Gemini, so it costs money (a few cents on a small model).
eval-live: check-model
> go run ./cmd/eval -llm record -case-filter attack -model $(MODEL) -recordings-dir recordings-live -case-timeout 300s -results-file evals/results/live-$(MODEL)-attacks.json
> @if grep -q 'GEMINI_ERROR\|"invalid_run": true' evals/results/live-$(MODEL)-attacks.json; then echo "INVALID: the run had errors, do not trust it"; exit 1; fi

docker-build:
> docker build -t $(IMAGE):$(VERSION) .

helm-lint:
> @if command -v helm >/dev/null 2>&1; then \
>   helm lint deploy/helm/log-analyzer --set gemini.existingSecret=x && \
>   helm template t deploy/helm/log-analyzer --set gemini.existingSecret=x >/dev/null; \
> else echo "helm not installed: skipping chart check"; fi

hooks:
> @printf '#!/bin/sh\nmake ci\n' > .git/hooks/pre-push && chmod +x .git/hooks/pre-push && echo "pre-push hook installed: make ci runs before every push"
