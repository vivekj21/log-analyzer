#!/usr/bin/env bash
# Accuracy test: runs every labelled broken app in accuracy/cases/ through the rule-based model
# and (if GEMINI_API_KEY is set) real Gemini, compares each answer with the known cause, prints a
# table and writes accuracy/RESULTS.md.   Run it with: make accuracy   (or GEMINI_API_KEY=... make accuracy)
#   WAIT=120        seconds to let the faults show up
#   K3D_CLUSTER=x   use this existing cluster instead of a throwaway one
set -u
cd "$(dirname "$0")/.."
export PATH="$HOME/.local/bin:$PATH"
for c in kubectl k3d docker go; do command -v "$c" >/dev/null || { echo "missing tool: $c"; exit 1; }; done
WAIT="${WAIT:-120}"
LIVE=0; [ -n "${GEMINI_API_KEY:-}" ] && LIVE=1
CASES=$(ls accuracy/cases/*.yaml | xargs -n1 basename | sed 's/\.yaml$//')
OUT=$(mktemp -d)
echo "== building analyzer"; go build -o "$OUT/analyzer" ./cmd/analyzer || exit 1

PREV=$(kubectl config current-context 2>/dev/null || true); CREATED=0
if [ -n "${K3D_CLUSTER:-}" ]; then
  NAME="$K3D_CLUSTER"
  docker ps --format '{{.Names}}' | grep -q "^k3d-${NAME}-server-0$" || { echo "cluster $NAME is not running"; exit 1; }
else
  NAME="la-acc-$$"
  echo "== creating throwaway cluster $NAME"
  k3d cluster create "$NAME" --servers 1 --agents 0 --no-lb --timeout 240s --k3s-arg "--disable=traefik@server:0" \
    || { k3d cluster delete "$NAME" >/dev/null 2>&1; exit 1; }
  CREATED=1
fi
cleanup() {
  for c in $CASES; do kubectl delete namespace "la-acc-$c" --wait=false >/dev/null 2>&1; done
  [ -n "$PREV" ] && kubectl config use-context "$PREV" >/dev/null 2>&1
  [ "$CREATED" = 1 ] && { echo "== deleting $NAME"; k3d cluster delete "$NAME" >/dev/null 2>&1; }
}
trap cleanup EXIT
kubectl config use-context "k3d-$NAME" >/dev/null || exit 1
case "$(kubectl config current-context)" in k3d-*) ;; *) echo "refusing: not a k3d context"; exit 1;; esac
kubectl wait --for=condition=Ready node --all --timeout=120s || exit 1

echo "== starting $(echo $CASES | wc -w) apps"
for c in $CASES; do
  kubectl create namespace "la-acc-$c" >/dev/null || exit 1
  kubectl apply -f "accuracy/cases/$c.yaml" >/dev/null || exit 1
done
echo "== waiting ${WAIT}s for the faults to show up"; sleep "$WAIT"
kubectl get pods -A 2>/dev/null | grep la-acc- || true

# the category the report committed to Git (first 20 lines hold it), or ERROR / NONE
answer() { # $1=case $2=mode
  local repo f cat
  repo=$(mktemp -d)
  if ! "$OUT/analyzer" --llm "$2" --namespace "la-acc-$1" --service "$1" --symptom "$SYMPTOM" \
       --since 30m --git-repo "$repo" --timeout 4m >"$OUT/$1-$2.log" 2>&1; then echo ERROR; return; fi
  f=$(find "$repo" -name '*.md' | head -1)
  [ -n "$f" ] || { echo NONE; return; }
  cat=$(sed -n 1,20p "$f" | grep -oE '"(OOM_KILLED|CRASHLOOP_BAD_CONFIG|IMAGE_PULL_FAIL|READINESS_PROBE_FAIL|DB_LOCK|NO_FAULT|CANNOT_DETERMINE)"' | head -1 | tr -d '"')
  echo "${cat:-NONE}"
}

R="$OUT/results.tsv"; : > "$R"; HEUR_OK=0; LIVE_OK=0; N=0
for c in $CASES; do
  TRUTH=$(sed -n 's/^# expect: *//p' "accuracy/cases/$c.yaml" | head -1)
  SYMPTOM=$(sed -n 's/^# symptom: *//p' "accuracy/cases/$c.yaml" | head -1)
  SIM=""; grep -q '^# simulated:' "accuracy/cases/$c.yaml" && SIM=" (simulated)"
  echo "== case $c (truth $TRUTH)"
  H=$(answer "$c" heuristic); L="-"
  [ "$LIVE" = 1 ] && L=$(answer "$c" live)
  N=$((N+1)); [ "$H" = "$TRUTH" ] && HEUR_OK=$((HEUR_OK+1)); [ "$L" = "$TRUTH" ] && LIVE_OK=$((LIVE_OK+1))
  printf '%s%s\t%s\t%s\t%s\n' "$c" "$SIM" "$TRUTH" "$H" "$L" >> "$R"
done

mark() { [ "$1" = "$2" ] && echo "✅ $1" || { [ "$1" = "-" ] && echo "-" || echo "❌ $1"; }; }
MODEL="${GEMINI_MODEL:-built-in default}"
{
  echo "# Accuracy"
  echo
  echo "Run on $(date -u +%F) at commit \`$(git rev-parse --short HEAD)\`. Each case is a small broken app on a throwaway k3d cluster with a known cause."
  echo "A case is right only if the answer's category equals the known cause. \`ERROR\`/\`NONE\` mean the run failed, and count as wrong."
  echo
  echo "| App | Known cause | Rule-based model | Gemini (\`$MODEL\`) |"
  echo "|---|---|---|---|"
  while IFS=$'\t' read -r a t h l; do echo "| $a | $t | $(mark "$h" "$t") | $(mark "$l" "$t") |"; done < "$R"
  echo
  echo "**Rule-based: $HEUR_OK of $N.**$([ "$LIVE" = 1 ] && echo " **Gemini: $LIVE_OK of $N.**" || echo " Gemini was not run (no GEMINI_API_KEY).")"
  echo
  echo "## What this does not show"
  echo "- $N cases is a small sample, not a benchmark. One run each; AI answers can differ between runs."
  echo "- The cases are simple: one broken app alone in its namespace. Real incidents are messier."
  echo "- \`dblock\` is simulated: the app only prints database-lock errors, there is no real database."
  echo "- The cases were written by the author of the tool."
} > accuracy/RESULTS.md
echo; cat accuracy/RESULTS.md
echo; echo "logs saved in $OUT; table written to accuracy/RESULTS.md"
