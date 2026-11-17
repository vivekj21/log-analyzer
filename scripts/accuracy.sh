#!/usr/bin/env bash
# Accuracy test: runs every labelled broken app in accuracy/cases/ through the rule-based model and
# through each model service you list, compares each answer with the known cause, prints a table
# and writes accuracy/RESULTS.md.   Run it with: make accuracy
#
#   WAIT=120        seconds to let the faults show up
#   K3D_CLUSTER=x   use this existing cluster instead of a throwaway one
#   ACC_MODELS      which model services to test, space separated, each written as
#                   label|provider|model|base_url|NAME_OF_THE_ENV_VAR_HOLDING_THE_KEY
#                   Example:
#                   ACC_MODELS="gemini|gemini|||GEMINI_API_KEY groq|openai|llama-3.3-70b-versatile|https://api.groq.com/openai/v1|GROQ_API_KEY claude|anthropic|<model name>||ANTHROPIC_API_KEY"
#                   If unset and GEMINI_API_KEY is set, only Gemini is tested. Leave model or base_url empty to use the default.
set -u
cd "$(dirname "$0")/.."
export PATH="$HOME/.local/bin:$PATH"
for c in kubectl k3d docker go; do command -v "$c" >/dev/null || { echo "missing tool: $c"; exit 1; }; done
WAIT="${WAIT:-120}"
if [ -z "${ACC_MODELS:-}" ] && [ -n "${GEMINI_API_KEY:-}" ]; then ACC_MODELS="gemini|gemini|${GEMINI_MODEL:-}||GEMINI_API_KEY"; fi
LABELS=(); SPECS=()
for spec in ${ACC_MODELS:-}; do
  IFS='|' read -r label prov model base keyenv <<< "$spec"
  [ -n "$label" ] && [ -n "$prov" ] && [ -n "$keyenv" ] || { echo "bad ACC_MODELS entry: $spec (want label|provider|model|base_url|KEY_ENV_VAR)"; exit 1; }
  if [ -z "${!keyenv:-}" ]; then echo "skipping $label: environment variable $keyenv is not set"; continue; fi
  LABELS+=("$label"); SPECS+=("$spec")
done
CASES=$(ls accuracy/cases/*.yaml | xargs -n1 basename | sed 's/\.yaml$//')
OUT=$(mktemp -d)
echo "== building analyzer"; go build -o "$OUT/analyzer" ./cmd/analyzer || exit 1

# Try each model service once, cheaply, before the long run: a wrong key or model name should cost seconds, not 15 minutes.
GOOD_LABELS=(); GOOD_SPECS=()
for spec in "${SPECS[@]:-}"; do
  [ -n "$spec" ] || continue
  IFS='|' read -r label prov model base keyenv <<< "$spec"
  args=(--provider "$prov"); [ -n "$model" ] && args+=(--model "$model"); [ -n "$base" ] && args+=(--base-url "$base")
  echo "== test call to $label"
  if LLM_API_KEY="${!keyenv}" "$OUT/analyzer" "${args[@]}" --namespace preflight --dry-run --git-repo "$(mktemp -d)" --timeout 90s >"$OUT/preflight-$label.log" 2>&1; then
    GOOD_LABELS+=("$label"); GOOD_SPECS+=("$spec")
  else
    echo "skipping $label: its test call failed. Last lines:"; tail -4 "$OUT/preflight-$label.log" | cut -c1-200
  fi
done
LABELS=("${GOOD_LABELS[@]:-}"); SPECS=("${GOOD_SPECS[@]:-}")

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
answer() { # $1=case $2=column name  rest = analyzer arguments; the key (if any) is in LLM_API_KEY
  local repo f cat c=$1 col=$2; shift 2
  repo=$(mktemp -d)
  if ! "$OUT/analyzer" "$@" --namespace "la-acc-$c" --service "$c" --symptom "$SYMPTOM" \
       --since 30m --git-repo "$repo" --timeout 4m >"$OUT/$c-$col.log" 2>&1; then echo ERROR; return; fi
  f=$(find "$repo" -name '*.md' | head -1)
  [ -n "$f" ] || { echo NONE; return; }
  cat=$(sed -n 1,20p "$f" | grep -oE '"(OOM_KILLED|CRASHLOOP_BAD_CONFIG|IMAGE_PULL_FAIL|READINESS_PROBE_FAIL|DB_LOCK|NO_FAULT|CANNOT_DETERMINE)"' | head -1 | tr -d '"')
  echo "${cat:-NONE}"
}

R="$OUT/results.tsv"; : > "$R"; N=0; HEUR_OK=0
OK=(); for _ in "${LABELS[@]:-}"; do OK+=(0); done
for c in $CASES; do
  TRUTH=$(sed -n 's/^# expect: *//p' "accuracy/cases/$c.yaml" | head -1)
  SYMPTOM=$(sed -n 's/^# symptom: *//p' "accuracy/cases/$c.yaml" | head -1)
  SIM=""; grep -q '^# simulated:' "accuracy/cases/$c.yaml" && SIM=" (simulated)"
  echo "== case $c (truth $TRUTH)"
  H=$(answer "$c" rules --llm heuristic)
  N=$((N+1)); [ "$H" = "$TRUTH" ] && HEUR_OK=$((HEUR_OK+1))
  ROW="$c$SIM"$'\t'"$TRUTH"$'\t'"$H"
  i=0
  for spec in "${SPECS[@]:-}"; do
    [ -n "$spec" ] || continue
    IFS='|' read -r label prov model base keyenv <<< "$spec"
    args=(--provider "$prov"); [ -n "$model" ] && args+=(--model "$model"); [ -n "$base" ] && args+=(--base-url "$base")
    A=$(LLM_API_KEY="${!keyenv}" answer "$c" "$label" "${args[@]}")
    [ "$A" = "$TRUTH" ] && OK[$i]=$((OK[$i]+1))
    ROW="$ROW"$'\t'"$A"
    i=$((i+1))
  done
  printf '%s\n' "$ROW" >> "$R"
done

mark() { [ "$1" = "$2" ] && echo "✅ $1" || echo "❌ $1"; }
{
  echo "# Accuracy"
  echo
  echo "Run on $(date -u +%F) at commit \`$(git rev-parse --short HEAD)\`. Each case is a small broken app on a throwaway k3d cluster with a known cause."
  echo "A case is right only if the answer's category equals the known cause. \`ERROR\`/\`NONE\` mean the run failed, and count as wrong."
  echo
  HDR="| App | Known cause | Rule-based model |"; SEP="|---|---|---|"
  i=0; for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue
    IFS='|' read -r label prov model base keyenv <<< "$spec"
    HDR="$HDR $label (\`$prov${model:+/$model}\`) |"; SEP="$SEP---|"; i=$((i+1))
  done
  echo "$HDR"; echo "$SEP"
  while IFS=$'\t' read -r -a F; do
    line="| ${F[0]} | ${F[1]} | $(mark "${F[2]}" "${F[1]}") |"
    for ((k=3; k<${#F[@]}; k++)); do line="$line $(mark "${F[$k]}" "${F[1]}") |"; done
    echo "$line"
  done < "$R"
  echo
  SUM="**Rule-based: $HEUR_OK of $N.**"
  i=0; for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue
    IFS='|' read -r label _ <<< "$spec"; SUM="$SUM **$label: ${OK[$i]} of $N.**"; i=$((i+1))
  done
  [ ${#SPECS[@]} -eq 0 ] && SUM="$SUM No model service was tested (set ACC_MODELS or GEMINI_API_KEY)."
  echo "$SUM"
  echo
  echo "## What this does not show"
  echo "- $N cases is a small sample, not a benchmark. One run each. Results can differ between runs: AI answers vary, and the rule-based model depends on what the cluster looks like at the moment it is checked."
  echo "- The cases are simple: one broken app alone in its namespace. Real incidents are messier."
  echo "- \`dblock\` is simulated: the app only prints database-lock errors, there is no real database."
  echo "- The cases were written by the author of the tool."
} > accuracy/RESULTS.md
echo; cat accuracy/RESULTS.md
echo; echo "logs saved in $OUT; table written to accuracy/RESULTS.md"
