#!/usr/bin/env bash
# Installs the Helm chart on a throwaway k3d cluster and checks the result.
# Free: uses the rule-based model, no API key, no Gemini calls.
set -u
cd "$(dirname "$0")/.."
export PATH="$HOME/.local/bin:$PATH"
for c in kubectl k3d docker curl; do command -v "$c" >/dev/null || { echo "missing tool: $c"; exit 1; }; done
if ! command -v helm >/dev/null; then
  echo "== installing helm v3.16.4 into ~/.local/bin"
  mkdir -p ~/.local/bin; t=$(mktemp -d)
  curl -fsSL https://get.helm.sh/helm-v3.16.4-linux-amd64.tar.gz | tar -xz -C "$t" && mv "$t/linux-amd64/helm" ~/.local/bin/helm || { echo "helm download failed"; exit 1; }
fi
helm version --short
IMG=log-analyzer:0.1.0
echo "== building image"; docker build -q -t "$IMG" . >/dev/null || { echo "docker build failed"; exit 1; }
NAME="la-helm-$$"; PREV=$(kubectl config current-context 2>/dev/null || true)
echo "== creating throwaway cluster $NAME"
k3d cluster create "$NAME" --servers 1 --agents 0 --no-lb --timeout 240s --k3s-arg "--disable=traefik@server:0" || { k3d cluster delete "$NAME" >/dev/null 2>&1; exit 1; }
cleanup() { [ -n "$PREV" ] && kubectl config use-context "$PREV" >/dev/null 2>&1; echo "== deleting $NAME"; k3d cluster delete "$NAME"; }
trap cleanup EXIT
kubectl config use-context "k3d-$NAME" >/dev/null
case "$(kubectl config current-context)" in k3d-*) ;; *) echo "refusing: not a k3d context"; exit 1;; esac
kubectl wait --for=condition=Ready node --all --timeout=120s || exit 1
kubectl apply -f - <<'YAML'
apiVersion: v1
kind: Namespace
metadata:
  name: la-demo
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: config-demo
  namespace: la-demo
spec:
  replicas: 1
  selector:
    matchLabels: {app: config-demo}
  template:
    metadata:
      labels: {app: config-demo}
    spec:
      containers:
        - name: app
          image: busybox:1.36
          command: ["sh", "-c", "echo 'FATAL: required setting DATABASE_URL is missing, invalid configuration' >&2; exit 1"]
YAML
k3d image import "$IMG" -c "$NAME" || exit 1
echo "== waiting 60s for the fault to show up"; sleep 60
kubectl get pods -n la-demo
helm install inv1 deploy/helm/log-analyzer --set llm.mode=heuristic \
  --set investigation.namespace=la-demo --set investigation.service=config-demo \
  --set investigation.symptom="pod in CrashLoopBackOff right after deploy" || exit 1
for i in $(seq 1 60); do
  s=$(kubectl get job inv1 -o jsonpath='{.status.succeeded}'); f=$(kubectl get job inv1 -o jsonpath='{.status.failed}')
  { [ "${s:-0}" -ge 1 ] || [ "${f:-0}" -ge 1 ]; } && break; sleep 3
done
kubectl logs job/inv1 > /tmp/helm-k3d-job.log 2>&1
tail -40 /tmp/helm-k3d-job.log
FAIL=0
grep -q "ROOT_CAUSE_CATEGORY: CRASHLOOP_BAD_CONFIG" /tmp/helm-k3d-job.log && echo "CHECK OK: category" || { echo "CHECK FAILED: category"; FAIL=1; }
grep -q "Recorded & committed" /tmp/helm-k3d-job.log && echo "CHECK OK: committed" || { echo "CHECK FAILED: not committed"; FAIL=1; }
[ "$FAIL" = 0 ] && echo "HELM E2E PASS" || echo "HELM E2E FAIL"
exit "$FAIL"
