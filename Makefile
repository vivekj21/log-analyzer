.RECIPEPREFIX = >
MODEL ?= $(if $(GEMINI_MODEL),$(GEMINI_MODEL),$(shell sed -n 's/.*DefaultModel = "\(.*\)"/\1/p' internal/llm/defaults.go))
EVAL_OUT ?= /tmp/log-analyzer-eval-offline.json
IMAGE ?= log-analyzer
VERSION ?= 0.1.0

.PHONY: fetch-samples check-cases validate-cases check-providers check-accuracy-run check-accuracy-report check-commit-results commit-results check-redaction accuracy helm-k3d-live ci fmt-check vet build test race eval-offline check-model eval-live docker-build helm-lint helm-k3d hooks

# Everything GitHub runs. Needs no API key and makes no paid calls.
ci: eval-offline fmt-check vet build test race check-redaction check-providers check-cases check-accuracy-run check-accuracy-report check-commit-results helm-lint check-helm-rbac

fmt-check:
> @test -z "$$(gofmt -l .)" || { echo "gofmt needed for:"; gofmt -l .; exit 1; }

vet:
> go vet ./...

build:
> go build ./...

# Third-party sample logs (Loghub) are downloaded, not stored in the repository. See testdata/README.md.
fetch-samples:
> scripts/fetch-loghub-samples.sh

test: fetch-samples
> LOG_ANALYZER_REQUIRE_SAMPLES=1 go test ./...

race: fetch-samples
> LOG_ANALYZER_REQUIRE_SAMPLES=1 go test -race ./...

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
> @if grep -q 'GEMINI_ERROR\|MODEL_ERROR\|"invalid_run": true' evals/results/live-$(MODEL)-attacks.json; then echo "INVALID: the run had errors, do not trust it"; exit 1; fi

docker-build:
> docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

helm-lint:
> @if command -v helm >/dev/null 2>&1; then \
>   helm lint deploy/helm/log-analyzer --set gemini.existingSecret=x && \
>   helm template t deploy/helm/log-analyzer --set gemini.existingSecret=x >/dev/null; \
> else echo "helm not installed: skipping chart check"; fi
> @if command -v helm >/dev/null 2>&1; then \
>   if helm template t deploy/helm/log-analyzer --set llm.mode=live --set gemini.existingSecret=x >/dev/null 2>&1; then echo "FAIL: live mode installed without acknowledging that data leaves the cluster"; exit 1; fi; \
>   helm template t deploy/helm/log-analyzer --set llm.mode=live --set gemini.existingSecret=x --set llm.acknowledgeDataLeavesCluster=true >/dev/null || { echo "FAIL: live mode with acknowledgement should render"; exit 1; }; \
>   if helm template t deploy/helm/log-analyzer --set llm.mode=bogus >/dev/null 2>&1; then echo "FAIL: a bad llm.mode was accepted"; exit 1; fi; \
>   OUT=$$(helm template t deploy/helm/log-analyzer --set llm.mode=live --set gemini.existingSecret=x --set llm.acknowledgeDataLeavesCluster=true); \
>   echo "$$OUT" | grep -q "LLM_API_KEY_FILE" || { echo "FAIL: live mode should pass the key as a mounted file"; exit 1; }; \
>   if echo "$$OUT" | grep -q "secretKeyRef"; then echo "FAIL: the key must not be passed as an environment variable"; exit 1; fi; \
>   if helm template t deploy/helm/log-analyzer --set llm.mode=live --set llm.provider=openai --set gemini.existingSecret=x --set llm.acknowledgeDataLeavesCluster=true >/dev/null 2>&1; then echo "FAIL: openai without llm.model was accepted"; exit 1; fi; \
>   helm template t deploy/helm/log-analyzer --set llm.mode=live --set llm.provider=openai --set llm.model=m --set llm.baseUrl=https://api.groq.com/openai/v1 --set llm.apiKeySecret=k --set llm.acknowledgeDataLeavesCluster=true | grep -q "LLM_BASE_URL" || { echo "FAIL: openai settings did not reach the pod"; exit 1; }; \
>   echo "helm-lint: live mode needs acknowledgement and mounts the key as a file; heuristic is the default"; fi

hooks:
> @printf '#!/bin/sh\nmake ci\n' > .git/hooks/pre-push && chmod +x .git/hooks/pre-push && echo "pre-push hook installed: make ci runs before every push"

.PHONY: k3d-e2e
k3d-e2e:
> ./scripts/k3d-e2e.sh

helm-k3d:
> ./scripts/helm-k3d.sh

check-providers:
> ./scripts/check-providers.sh

check-helm-rbac:
> ./scripts/check-helm-rbac.sh

check-cases:
> ./scripts/check-cases.sh

validate-cases:
> ACC_VALIDATE_ONLY=1 ./scripts/accuracy.sh

check-accuracy-run:
> ./scripts/check-accuracy-run.sh

check-accuracy-report:
> ./scripts/check-accuracy-report.sh

check-commit-results:
> ./scripts/check-commit-results.sh

commit-results:
> ./scripts/commit-results.sh

check-redaction:
> ./scripts/check-redaction.sh

accuracy:
> ./scripts/accuracy.sh

helm-k3d-live:
> LLM_MODE=live ./scripts/helm-k3d.sh
