#!/usr/bin/env bash
# Accuracy test: runs every labelled broken app in accuracy/cases/ through the rule-based model and
# through each model service you list, compares each answer with the known cause, prints a table
# and writes accuracy/RESULTS.md.   Run it with: make accuracy
#
#   WAIT=120        seconds to let the faults show up
#   ACC_CASES="healthy oom"  run only these cases. The table is printed and NOT saved to accuracy/RESULTS.md.
#   ACC_REPEAT=3    run each case this many times per model (to see how stable the answers are)
#   K3D_CLUSTER=x   use this existing cluster instead of a throwaway one
#   ACC_TIMEOUT=4m  time allowed for one answer (a slow or rate-limited service may need more, e.g. 10m)
#   ACC_SLOW="groq" labels (from ACC_MODELS) that get a pause of ACC_PAUSE seconds after each answer,
#   ACC_PAUSE=60    so a service with a low tokens-per-minute limit can recover between cases
#   ACC_MODELS      which model services to test, space separated, each written as
#                   label|provider|model|base_url|NAME_OF_THE_ENV_VAR_HOLDING_THE_KEY
#                   Example:
#                   ACC_MODELS="gemini|gemini|||GEMINI_API_KEY groq|openai|llama-3.3-70b-versatile|https://api.groq.com/openai/v1|GROQ_API_KEY claude|anthropic|<model name>||ANTHROPIC_API_KEY"
#                   If unset and GEMINI_API_KEY is set, only Gemini is tested. Leave model or base_url empty to use the default.
set -u
cd "$(dirname "$0")/.."
. scripts/accuracy-report.sh
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
CASES="${ACC_CASES:-$(ls accuracy/cases/*.yaml | xargs -n1 basename | sed 's/\.yaml$//')}"
for c in $CASES; do [ -f "accuracy/cases/$c.yaml" ] || { echo "no such case: $c (see accuracy/cases/)"; exit 1; }; done
REPEAT="${ACC_REPEAT:-1}"; case "$REPEAT" in ''|*[!0-9]*|0) echo "ACC_REPEAT must be a whole number, 1 or more"; exit 1;; esac
OUT=$(mktemp -d)
echo "== building analyzer"; go build -o "$OUT/analyzer" ./cmd/analyzer || exit 1

# Try each model service once, cheaply (one short log line, no cluster), before the long run: a wrong key or model name should cost seconds, not 15 minutes.
printf '%s\n' "2026-01-01T00:00:00Z ERROR connection refused: db:5432" > "$OUT/preflight-input.log"
GOOD_LABELS=(); GOOD_SPECS=()
for spec in "${SPECS[@]:-}"; do
  [ -n "$spec" ] || continue
  IFS='|' read -r label prov model base keyenv <<< "$spec"
  args=(--provider "$prov"); [ -n "$model" ] && args+=(--model "$model"); [ -n "$base" ] && args+=(--base-url "$base")
  echo "== test call to $label"
  if LLM_API_KEY="${!keyenv}" "$OUT/analyzer" "${args[@]}" --log-file "$OUT/preflight-input.log" --dry-run --git-repo "$(mktemp -d)" --timeout 90s >"$OUT/preflight-$label.log" 2>&1; then
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
       --since 30m --git-repo "$repo" --timeout "${ACC_TIMEOUT:-4m}" >"$OUT/$c-$col-$RUN.log" 2>&1; then echo ERROR; return; fi
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
  echo "== case $c (truth $TRUTH), $REPEAT run(s) per model"
  N=$((N+1))
  HL=""; ML=(); for _ in "${LABELS[@]:-}"; do ML+=(""); done
  for ((RUN=1; RUN<=REPEAT; RUN++)); do
    H=$(answer "$c" rules --llm heuristic)
    [ "$H" = "$TRUTH" ] && HEUR_OK=$((HEUR_OK+1))
    HL="${HL:+$HL,}$H"
    i=0
    for spec in "${SPECS[@]:-}"; do
      [ -n "$spec" ] || continue
      IFS='|' read -r label prov model base keyenv <<< "$spec"
      args=(--provider "$prov"); [ -n "$model" ] && args+=(--model "$model"); [ -n "$base" ] && args+=(--base-url "$base")
      A=$(LLM_API_KEY="${!keyenv}" answer "$c" "$label" "${args[@]}")
      [ "$A" = "$TRUTH" ] && OK[$i]=$((OK[$i]+1))
      ML[$i]="${ML[$i]:+${ML[$i]},}$A"
      case " ${ACC_SLOW:-} " in *" $label "*) sleep "${ACC_PAUSE:-0}";; esac
      i=$((i+1))
    done
  done
  ROW="$c$SIM"$'\t'"$TRUTH"$'\t'"$HL"
  i=0; for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue; ROW="$ROW"$'\t'"${ML[$i]}"; i=$((i+1)); done
  printf '%s\n' "$ROW" >> "$R"
done

TOTAL=$((N*REPEAT))
if [ -n "${ACC_CASES:-}" ]; then DEST="$OUT/partial-results.md"; else DEST=accuracy/RESULTS.md; fi
write_report > "$DEST"
echo; cat "$DEST"
echo; echo "logs saved in $OUT; table written to $DEST"
