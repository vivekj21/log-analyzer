#!/usr/bin/env bash
# End-to-end test of Log Analyzer on a local k3d cluster.
# Free by default: it uses the heuristic model, so no Gemini calls are made.
#   LIVE=1       also run one real Gemini investigation (costs a little)
#   AUTOSTOP=1   stop the cluster afterwards (saves battery)
#   KEEP=1       keep the demo workloads afterwards
#   K3D_CLUSTER  use this existing cluster instead of a throwaway one
#   KEEP_CLUSTER=1  do not delete the throwaway cluster afterwards
set -u
cd "$(dirname "$0")/.."
for c in kubectl k3d docker go; do command -v "$c" >/dev/null || { echo "missing tool: $c"; exit 1; }; done
echo "== environment"; k3d version | head -2; k3d cluster list
PREV=$(kubectl config current-context 2>/dev/null || true)
CREATED=0; STARTED=0
if [ -n "${K3D_CLUSTER:-}" ]; then
  NAME="$K3D_CLUSTER"
  if ! docker ps --format '{{.Names}}' | grep -q "^k3d-${NAME}-server-0$"; then
    echo "== cluster $NAME is stopped: starting it"; k3d cluster start "$NAME" || exit 1; STARTED=1
  fi
else
  NAME="la-e2e-$$"
  echo "== creating throwaway cluster $NAME"
  if ! k3d cluster create "$NAME" --servers 1 --agents 0 --no-lb --timeout 240s --k3s-arg "--disable=traefik@server:0"; then
    echo "== could not create a cluster. Docker state:"; docker network ls | head; docker ps -a | head
    k3d cluster delete "$NAME" >/dev/null 2>&1
    exit 1
  fi
  CREATED=1
fi
cleanup() {
  [ "${KEEP:-0}" = 1 ] || kubectl delete namespace la-demo --wait=false >/dev/null 2>&1
  [ -n "$PREV" ] && kubectl config use-context "$PREV" >/dev/null 2>&1
  if [ "$CREATED" = 1 ] && [ "${KEEP_CLUSTER:-0}" != 1 ]; then
    echo "== deleting throwaway cluster $NAME"; k3d cluster delete "$NAME"
  elif [ "$STARTED" = 1 ] || [ "${AUTOSTOP:-0}" = 1 ]; then
    echo "== stopping cluster $NAME"; k3d cluster stop "$NAME"
  fi
}
trap cleanup EXIT
kubectl config use-context "k3d-$NAME" >/dev/null || exit 1
case "$(kubectl config current-context)" in k3d-*) ;; *) echo "refusing to run: not a k3d context"; exit 1;; esac
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
  name: oom-demo
  namespace: la-demo
spec:
  replicas: 1
  selector:
    matchLabels: {app: oom-demo}
  template:
    metadata:
      labels: {app: oom-demo}
    spec:
      containers:
        - name: stress
          image: polinux/stress
          command: ["stress"]
          args: ["--vm", "1", "--vm-bytes", "200M", "--vm-hang", "1"]
          resources:
            requests: {memory: 20Mi}
            limits: {memory: 50Mi}
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
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: image-demo
  namespace: la-demo
spec:
  replicas: 1
  selector:
    matchLabels: {app: image-demo}
  template:
    metadata:
      labels: {app: image-demo}
    spec:
      containers:
        - name: app
          image: registry.invalid/log-analyzer-demo/nope:1.0
YAML
echo "== waiting 100s for the faults to show up"; sleep 100
kubectl get pods -n la-demo

OUT=$(mktemp -d); FAIL=0
run() {
  local name=$1 svc=$2 symptom=$3 mode=${4:-heuristic} repo f
  repo=$(mktemp -d)
  echo; echo "=== scenario: $name (model mode: $mode)"
  if go run ./cmd/analyzer/main.go --llm "$mode" --namespace la-demo --service "$svc" \
      --symptom "$symptom" --since 30m --git-repo "$repo" --timeout 4m >"$OUT/$name-$mode.log" 2>&1; then
    grep -E "Severity|Root Cause" "$OUT/$name-$mode.log" | head -3
    f=$(find "$repo" -name '*.md' | head -1)
    if [ -n "$f" ]; then echo "--- report excerpt"; sed -n 1,25p "$f"; else echo "NO REPORT FILE"; FAIL=1; fi
  else
    echo "FAILED, last log lines:"; tail -15 "$OUT/$name-$mode.log"; FAIL=1
  fi
}
run oom oom-demo "pod keeps restarting and being killed"
run config config-demo "pod in CrashLoopBackOff right after deploy"
run image image-demo "pod stuck and never starts"
if [ "${LIVE:-0}" = 1 ]; then
  if [ -n "${GEMINI_API_KEY:-}" ]; then run oom oom-demo "pod keeps restarting and being killed" live; else echo "LIVE=1 needs GEMINI_API_KEY"; fi
fi
echo; echo "logs saved in $OUT"
exit $FAIL
