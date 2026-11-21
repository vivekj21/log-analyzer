#!/usr/bin/env bash
# Accuracy test: runs every labelled broken app in accuracy/cases/ through the rule-based model and
# through each model service you list, compares each answer with the known cause, prints a table
# and writes accuracy/RESULTS.md.   Run it with: make accuracy
#
#   WAIT=120        seconds to let the faults show up
#   ACC_CASES="healthy oom"  run only these cases. The table is printed and NOT saved to accuracy/RESULTS.md.
#   ACC_SET=heldout run only the cases of one set: dev (used while tuning) or heldout (never tuned against).
#                   Partial runs are printed, not saved to accuracy/RESULTS.md.
#   ACC_REPEAT=3    run each case this many times per model (to see how stable the answers are)
#   ACC_VERIFY_WAIT=180  seconds to keep checking that each fault really showed up (the "# verify:" line of the case).
#                   A case whose fault does not show up is NOT scored (it is listed as "not scored"), so a broken test app never counts against a model.
#   ACC_VERIFY=0    skip that check
#   ACC_VALIDATE_ONLY=1  only start the apps and check that every fault shows up. No model is called, no key is needed. (make validate-cases)
#   ACC_CASES_DIR   where the case files are (default accuracy/cases)
#   K3D_CLUSTER=x   use this existing cluster instead of a throwaway one
#   ACC_TIMEOUT=4m  time allowed for one answer (a slow or rate-limited service may need more, e.g. 10m)
#   ACC_SLOW="groq" labels (from ACC_MODELS) that get a pause of ACC_PAUSE seconds after each answer,
#   ACC_PAUSE=60    so a service with a low tokens-per-minute limit can recover between cases (both are the default)
#   ACC_LOGS_DIR    where each run keeps its logs (default ~/log-analyzer-accuracy-logs/<date-time>/)
#   A failed run is labelled with its reason in errors.txt and on the scorecard: rate_limit (limit or quota reached), network
#   (service not reachable), auth (key refused), timeout, other. A network failure is retried twice (after 20s, then 60s;
#   ACC_RETRY_WAITS="20 60", use "" for none) before it counts. After 3 failed runs in a row, a model is not called again
#   in this run (its remaining cells show ERROR and errors.txt says "skipped ... last reason: ..."), so a used-up quota does not cost an hour.
#   Answers are kept (in ACC_CELLS_DIR, default <ACC_LOGS_DIR>/cells) per commit, case, model and run number. A later run at the
#   same commit reuses them and only asks for what is missing: a partial run is not wasted, and a full run after a
#   held-out run only does the rest. Failed runs are never kept. Not used when the working tree has uncommitted changes.
#   ACC_REUSE=0     do not reuse or keep answers
#   ACC_MODELS      which model services to test, space separated, each written as
#                   label|provider|model|base_url|NAME_OF_THE_ENV_VAR_HOLDING_THE_KEY
#                   Example:
#                   ACC_MODELS="gemini|gemini|||GEMINI_API_KEY groq|openai|llama-3.3-70b-versatile|https://api.groq.com/openai/v1|GROQ_API_KEY claude|anthropic|<model name>||ANTHROPIC_API_KEY"
#                   If unset and GEMINI_API_KEY is set, only Gemini is tested. Leave model or base_url empty to use the default.
set -u
cd "$(dirname "$0")/.."
CASES_DIR="${ACC_CASES_DIR:-accuracy/cases}"; export CASES_DIR
. scripts/accuracy-report.sh
. scripts/case-lib.sh
export PATH="$HOME/.local/bin:$PATH"
VALIDATE_ONLY="${ACC_VALIDATE_ONLY:-0}"
need="kubectl k3d docker go"; [ "$VALIDATE_ONLY" = 1 ] && need="kubectl k3d docker"
for c in $need; do command -v "$c" >/dev/null || { echo "missing tool: $c"; exit 1; }; done
WAIT="${WAIT:-120}"
if [ -z "${ACC_MODELS:-}" ] && [ -n "${GEMINI_API_KEY:-}" ]; then ACC_MODELS="gemini|gemini|${GEMINI_MODEL:-}||GEMINI_API_KEY"; fi
LABELS=(); SPECS=()
for spec in ${ACC_MODELS:-}; do
  IFS='|' read -r label prov model base keyenv <<< "$spec"
  [ -n "$label" ] && [ -n "$prov" ] && [ -n "$keyenv" ] || { echo "bad ACC_MODELS entry: $spec (want label|provider|model|base_url|KEY_ENV_VAR)"; exit 1; }
  if [ -z "${!keyenv:-}" ]; then echo "skipping $label: environment variable $keyenv is not set"; continue; fi
  LABELS+=("$label"); SPECS+=("$spec")
done
CASES="${ACC_CASES:-$(ls "$CASES_DIR"/*.yaml | xargs -n1 basename | sed 's/\.yaml$//')}"
for c in $CASES; do [ -f "$CASES_DIR/$c.yaml" ] || { echo "no such case: $c (see $CASES_DIR/)"; exit 1; }; done
if [ -n "${ACC_SET:-}" ]; then
  case "$ACC_SET" in dev|heldout) ;; *) echo "ACC_SET must be dev or heldout"; exit 1;; esac
  PICK=""; for c in $CASES; do [ "$(case_meta "$c" set)" = "$ACC_SET" ] && PICK="$PICK $c"; done
  CASES="${PICK# }"; [ -n "$CASES" ] || { echo "no cases in set $ACC_SET"; exit 1; }
  ACC_CASES="$CASES"
fi
REPEAT="${ACC_REPEAT:-1}"; case "$REPEAT" in ''|*[!0-9]*|0) echo "ACC_REPEAT must be a whole number, 1 or more"; exit 1;; esac
# Logs go to a permanent folder (not /tmp, which Ubuntu empties on reboot), so a failed run can still be read days later.
LOGROOT="${ACC_LOGS_DIR:-$HOME/log-analyzer-accuracy-logs}"
OUT="$LOGROOT/$(date +%Y%m%d-%H%M%S)"; mkdir -p "$OUT" || exit 1
: > "$OUT/errors.txt"; : > "$OUT/reused.txt"
# Answers are kept per commit so a partial run is not wasted. ACC_FORCE_COMMIT is only for the tests of this script.
CELLS="${ACC_CELLS_DIR:-$LOGROOT/cells}"; REUSE=0; COMMIT=""
if [ "${ACC_REUSE:-1}" = 1 ] && [ "$VALIDATE_ONLY" != 1 ]; then
  if [ -n "${ACC_FORCE_COMMIT:-}" ]; then COMMIT="$ACC_FORCE_COMMIT"; REUSE=1
  elif [ -z "$(git status --porcelain 2>/dev/null)" ] && COMMIT=$(git rev-parse HEAD 2>/dev/null); then REUSE=1
  else echo "== note: the working tree has uncommitted changes, so earlier answers are not reused and these are not kept"; fi
  [ "$REUSE" = 1 ] && mkdir -p "$CELLS"
fi
cell_key() { printf '%s' "$COMMIT/$(sha1sum < "$CASES_DIR/$1.yaml" | cut -c1-12)/$2" | sha1sum | cut -c1-20; }
# Why a run failed, from the last line of its log only (earlier lines are the investigation, which may mention quotas or timeouts)
failure_class() {
  local t; t=$(grep -v '^[[:space:]]*$' "$1" | tail -1)
  if   grep -Eiq '429|rate.?limit|quota|RESOURCE_EXHAUSTED|tokens per (day|minute)|too many requests' <<< "$t"; then echo rate_limit
  elif grep -Eiq 'dial tcp|lookup [^ ]+|no such host|connection (refused|reset)|i/o timeout|TLS handshake|network is unreachable|temporary failure in name resolution|unexpected EOF' <<< "$t"; then echo network
  elif grep -Eiq '\b(401|403)\b|unauthorized|api key|permission denied|invalid.*key' <<< "$t"; then echo auth
  elif grep -Eiq 'deadline exceeded|timed out|timeout' <<< "$t"; then echo timeout
  else echo other; fi
}
if [ "$VALIDATE_ONLY" = 1 ]; then SPECS=(); LABELS=(); else
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
fi

ALL_CASES="$CASES"
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
  for c in $ALL_CASES; do
    for n in "la-acc-$c" $(extra_namespaces "$c"); do kubectl delete namespace "$n" --wait=false >/dev/null 2>&1; done
  done
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
  kubectl apply -f "$CASES_DIR/$c.yaml" >/dev/null || exit 1
done
echo "== waiting ${WAIT}s for the faults to show up"; sleep "$WAIT"
kubectl get pods -A 2>/dev/null | grep la-acc- || true

# Check that each fault really showed up. A case whose fault is not visible is not scored: it would blame the model for a broken test app.
VALID="$CASES"; SKIPPED=""
if [ "${ACC_VERIFY:-1}" = 1 ]; then
  echo "== checking that each fault really showed up (up to ${ACC_VERIFY_WAIT:-180}s in all)"
  PENDING="$CASES"; VALID=""; END=$((SECONDS + ${ACC_VERIFY_WAIT:-180}))
  while :; do
    STILL=""
    for c in $PENDING; do
      verify_case "$c" 0; rc=$?
      if [ "$rc" = 0 ]; then VALID="$VALID $c"; else STILL="$STILL $c"; fi
    done
    PENDING="${STILL# }"
    [ -z "$PENDING" ] && break
    [ "$SECONDS" -ge "$END" ] && break
    sleep "${ACC_VERIFY_POLL:-5}"
  done
  VALID="${VALID# }"; SKIPPED="$PENDING"
  for c in $VALID; do echo "   fault visible : $c"; done
  for c in $SKIPPED; do
    echo "   NOT VISIBLE   : $c  (will not be scored)"
    echo "      verify line : $(case_meta "$c" verify)"
    NS="la-acc-$c" k get pods 2>&1 | head -8 | sed 's/^/      /'
    NS="la-acc-$c" k get events --no-headers 2>&1 | tail -6 | cut -c1-200 | sed 's/^/      /'
    for p in $(NS="la-acc-$c" k get pods -o name 2>/dev/null | head -4); do
      echo "      -- $p last exit: $(NS="la-acc-$c" k get "$p" -o jsonpath='{.status.containerStatuses[0].lastState.terminated.reason} code {.status.containerStatuses[0].lastState.terminated.exitCode}, now: {.status.containerStatuses[0].state.waiting.reason}' 2>&1)"
      NS="la-acc-$c" k logs "$p" --all-containers --tail=4 2>&1 | cut -c1-160 | sed 's/^/         log: /'
    done
  done
fi
if [ "$VALIDATE_ONLY" = 1 ]; then
  echo; echo "$(echo $VALID | wc -w) of $(echo $CASES | wc -w) cases show their fault. Not visible: ${SKIPPED:-none}"
  [ -z "$SKIPPED" ]; exit
fi
CASES="$VALID"; [ -n "$SKIPPED" ] && ACC_CASES="$CASES"
[ -n "$CASES" ] || { echo "no case showed its fault: nothing to score"; exit 1; }

# the category the report committed to Git (first 20 lines hold it), or ERROR / NONE
answer() { # $1=case $2=column name  rest = analyzer arguments; the key (if any) is in LLM_API_KEY; CELL (if set) is where the answer is kept
  local repo f cat ex tries c=$1 col=$2 svc svcargs=() attempt=0 class waits=(${ACC_RETRY_WAITS-20 60}); shift 2
  if [ -n "${CELL:-}" ] && [ -s "$CELL" ]; then   # asked before at this commit: reuse
    cat=$(sed -n 1p "$CELL"); ex=$(sed -n 2p "$CELL")
    [ "$ex" = "-" ] || printf '%s\t%s\t%s\t%s\n' "$c" "$col" "$RUN" "$ex" >> "$OUT/explain.tsv"
    echo "$col" >> "$OUT/reused.txt"; echo "$cat"; return
  fi
  svc=$(case_meta "$c" service); svc="${svc:-$c}"; [ "$svc" = "-" ] || svcargs=(--service "$svc")
  while :; do
    repo=$(mktemp -d)
    if "$OUT/analyzer" "$@" --namespace "la-acc-$c" "${svcargs[@]}" --symptom "$SYMPTOM" \
         --since 30m --git-repo "$repo" --timeout "${ACC_TIMEOUT:-4m}" >"$OUT/$c-$col-$RUN.log" 2>&1; then break; fi
    class=$(failure_class "$OUT/$c-$col-$RUN.log")
    if [ "$class" = network ] && [ "$attempt" -lt "${#waits[@]}" ]; then sleep "${waits[$attempt]}"; attempt=$((attempt+1)); continue; fi
    tries=""; [ "$attempt" -gt 0 ] && tries=" (after $attempt retries)"
    printf '%s\n' "$c $col run $RUN: [$class]$tries $(grep -v '^[[:space:]]*$' "$OUT/$c-$col-$RUN.log" | tail -1 | cut -c1-400)" >> "$OUT/errors.txt"
    echo ERROR; return
  done
  f=$(find "$repo" -name '*.md' | head -1)
  [ -n "$f" ] || { echo NONE; return; }
  cat=$(sed -n 1,20p "$f" | grep -oE "\"($CATRE)\"" | head -1 | tr -d '"')
  ex=-
  if [ "$cat" = "$TRUTH" ]; then ex=$(explain_check "$c" "$f"); printf '%s\t%s\t%s\t%s\n' "$c" "$col" "$RUN" "$ex" >> "$OUT/explain.tsv"; fi
  if [ -n "${CELL:-}" ] && [ -n "$cat" ]; then mkdir -p "$(dirname "$CELL")"; printf '%s\n%s\n' "$cat" "$ex" > "$CELL"; cp "$f" "$CELL.md"; fi
  echo "${cat:-NONE}"
}

# the categories come from the Go code, so there is one list only
CATRE=$(grep -oE 'Category[A-Za-z]+ += "[A-Z_]+"' internal/llm/model.go | grep -oE '"[A-Z_]+"' | tr -d '"' | paste -sd'|')
[ -n "$CATRE" ] || { echo "cannot read the categories from internal/llm/model.go"; exit 1; }
R="$OUT/results.tsv"; E="$OUT/explain.tsv"; : > "$R"; : > "$E"; N=0; FAILS=(); LASTCLASS=()
for c in $CASES; do
  TRUTH=$(case_meta "$c" expect)
  SYMPTOM=$(case_meta "$c" symptom)
  SIM=""; grep -q '^# simulated:' "$CASES_DIR/$c.yaml" && SIM=" (simulated)"
  echo "== case $c (truth $TRUTH), $REPEAT run(s) per model"
  N=$((N+1))
  HL=""; ML=(); for _ in "${LABELS[@]:-}"; do ML+=(""); done
  for ((RUN=1; RUN<=REPEAT; RUN++)); do
    H=$(CELL="" answer "$c" rules --llm heuristic)
    HL="${HL:+$HL,}$H"
    i=0
    for spec in "${SPECS[@]:-}"; do
      [ -n "$spec" ] || continue
      IFS='|' read -r label prov model base keyenv <<< "$spec"
      args=(--provider "$prov"); [ -n "$model" ] && args+=(--model "$model"); [ -n "$base" ] && args+=(--base-url "$base")
      CELL=""; [ "$REUSE" = 1 ] && CELL="$CELLS/$(cell_key "$c" "$label|$prov|$model|$base")-$RUN"
      if [ "${FAILS[$i]:-0}" -ge 3 ]; then
        A=ERROR; echo "$c $label run $RUN: skipped (3 failures in a row; last reason: ${LASTCLASS[$i]:-other})" >> "$OUT/errors.txt"
      else
        kept=0; [ -n "$CELL" ] && [ -s "$CELL" ] && kept=1
        A=$(CELL="$CELL" LLM_API_KEY="${!keyenv}" answer "$c" "$label" "${args[@]}")
        case "$A" in
          ERROR|NONE) FAILS[$i]=$(( ${FAILS[$i]:-0} + 1 )); LASTCLASS[$i]=$(tail -1 "$OUT/errors.txt" | sed -n 's/^[^[]*\[\([a-z_]*\)\].*/\1/p');;
          *) FAILS[$i]=0;;
        esac
        [ "$kept" = 1 ] || case " ${ACC_SLOW-groq} " in *" $label "*) sleep "${ACC_PAUSE:-60}";; esac
      fi
      ML[$i]="${ML[$i]:+${ML[$i]},}$A"
      i=$((i+1))
    done
  done
  ROW="$c$SIM"$'\t'"$TRUTH"$'\t'"$HL"
  i=0; for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue; ROW="$ROW"$'\t'"${ML[$i]}"; i=$((i+1)); done
  printf '%s\n' "$ROW" >> "$R"
done

TOTAL=$((N*REPEAT))
if [ -n "${ACC_CASES:-}" ]; then DEST="$OUT/partial-results.md"; else DEST=accuracy/RESULTS.md; fi
ERRFILE="$OUT/errors.txt"
REUSED_NOTE=""
if [ -s "$OUT/reused.txt" ]; then
  REUSED_NOTE="Answers reused from earlier runs at this same commit (not asked again): $(sort "$OUT/reused.txt" | uniq -c | awk '{printf "%s%s %s", (NR>1?", ":""), $2, $1}')."
fi
write_report > "$DEST"
echo; cat "$DEST"
echo
if [ -s "$OUT/errors.txt" ]; then echo "== why runs failed (last log line of each):"; cut -c1-300 "$OUT/errors.txt"; echo; fi
[ "$REUSE" = 1 ] && echo "answers kept in $CELLS: a later run at this same commit reuses them and only asks for what is missing"
echo "logs saved in $OUT; table written to $DEST"
