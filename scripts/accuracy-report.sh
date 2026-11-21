#!/usr/bin/env bash
# The scorecard text. Sourced by accuracy.sh (real run) and by check-accuracy-report.sh (test with made-up results).
# Needs these variables: R (results file, tab separated: app, known cause, rule-based answers, then one column per model;
# several runs of one cell are separated by commas), SPECS (array of "label|provider|model|base_url|KEYENV"),
# N (number of cases), REPEAT (runs per case), TOTAL (N * REPEAT). Optional: ACC_CASES (a partial run),
# E (explanation checks, tab separated: case, column name, run, ok|missing: ...|claimed: ...), SKIPPED (cases whose fault never showed up),
# ERRFILE (errors.txt of the run), REUSED_NOTE (a sentence about reused answers),
# CASES_DIR (where the case files are; each has a "# set: dev|heldout" line).
#
# Every run lands in one of four groups:
#   right   the answer equals the known cause
#   unsure  the answer is CANNOT_DETERMINE ("I cannot tell"): not right, but not a wrong diagnosis either
#   wrong   any other category
#   failed  ERROR or NONE: the run produced no answer

CASES_DIR="${CASES_DIR:-accuracy/cases}"
row_case() { local n="$1"; echo "${n%% (*}"; } # row name without the " (simulated)" suffix
row_set() { sed -n 's/^# set: *//p' "$CASES_DIR/$(row_case "$1").yaml" 2>/dev/null | head -1; }

classify() { # $1 = answer, $2 = known cause
  case "$1" in
    "$2") echo right;;
    CANNOT_DETERMINE) echo unsure;;
    ERROR|NONE) echo failed;;
    *) echo wrong;;
  esac
}

# One table cell. $1 = answers of all runs, comma separated; $2 = the known cause.
cell() {
  local IFS=','; local -a a=($1); local x c right=0 unsure=0 wrong=0 failed=0 n=${#a[@]} sym txt=""
  for x in "${a[@]}"; do
    case "$(classify "$x" "$2")" in right) right=$((right+1));; unsure) unsure=$((unsure+1));; wrong) wrong=$((wrong+1));; failed) failed=$((failed+1));; esac
  done
  if [ "$n" -eq 1 ]; then
    if [ "$right" -eq 1 ]; then sym="✅"; elif [ "$unsure" -eq 1 ]; then sym="❔"; elif [ "$wrong" -eq 1 ]; then sym="❌"; else sym="⛔"; fi
    echo "$sym $1"; return
  fi
  if [ "$right" -eq "$n" ]; then sym="✅"; elif [ "$wrong" -gt 0 ]; then sym="❌"; elif [ "$failed" -gt 0 ]; then sym="⛔"; else sym="❔"; fi
  [ "$right" -gt 0 ] && txt="$right right"
  [ "$unsure" -gt 0 ] && txt="${txt:+$txt, }$unsure unsure"
  [ "$wrong" -gt 0 ] && txt="${txt:+$txt, }$wrong wrong"
  [ "$failed" -gt 0 ] && txt="${txt:+$txt, }$failed failed"
  echo "$sym $txt: ${1//,/, }"
}

# Summary for column $1 (0 = rule-based, 1.. = models), counted over every run of the cases in set $2 (dev or heldout).
# Prints nothing if that set has no cases.
column_summary() {
  local col=$1 want=$2 right=0 unsure=0 wrong=0 failed=0 x
  local -a F a
  while IFS=$'\t' read -r -a F; do
    [ "$(row_set "${F[0]}")" = "$want" ] || continue
    local IFS=','; a=(${F[$((col+2))]}); IFS=$'\t'
    for x in "${a[@]}"; do
      case "$(classify "$x" "${F[1]}")" in right) right=$((right+1));; unsure) unsure=$((unsure+1));; wrong) wrong=$((wrong+1));; failed) failed=$((failed+1));; esac
    done
  done < "$R"
  local tot=$((right+unsure+wrong+failed)); [ "$tot" -gt 0 ] || return 0
  local extra="" name="dev"; [ "$want" = heldout ] && name="held-out"
  [ "$unsure" -gt 0 ] && extra="$unsure unsure"
  [ "$wrong" -gt 0 ] && extra="${extra:+$extra, }$wrong wrong"
  [ "$failed" -gt 0 ] && extra="${extra:+$extra, }$failed failed"
  local unit=""; [ "${REPEAT:-1}" -gt 1 ] && unit=" runs"
  echo "$name $right of $tot$unit right${extra:+ ($extra)}"
}

# "dev ...; held-out ..." for one column
both_sets() { local a b; a=$(column_summary "$1" dev); b=$(column_summary "$1" heldout); echo "${a}${a:+${b:+; }}${b}"; }

# Explanation checks of column name $1: "X of Y right answers also passed the checklist"
explain_summary() {
  local ok bad
  ok=$(awk -F'\t' -v c="$1" '$2==c && $4=="ok"' "$E" 2>/dev/null | wc -l)
  bad=$(awk -F'\t' -v c="$1" '$2==c && $4!="ok"' "$E" 2>/dev/null | wc -l)
  [ $((ok+bad)) -gt 0 ] || { echo "no right answers to check"; return; }
  echo "$ok of $((ok+bad)) right answers also passed the checklist"
}

write_report() {
  local label prov model base keyenv HDR SEP line k i
  echo "# Accuracy"
  echo
  if [ -n "${ACC_CASES:-}" ]; then
    echo "**Partial run: only these cases were run: ${ACC_CASES}. This is not the full scorecard.**"
    echo
  fi
  echo "Run on $(date -u +%F) at commit \`$(git rev-parse --short HEAD 2>/dev/null || echo unknown)\`. Each case is a small broken app on a throwaway k3d cluster with a known cause."
  echo "A run is right (✅) only if the answer's category equals the known cause. \`CANNOT_DETERMINE\` (❔ unsure) means the model said it could not tell: not right, but not a wrong diagnosis. Any other category is wrong (❌). \`ERROR\`/\`NONE\` (⛔) mean the run failed."
  if [ "${REPEAT:-1}" -gt 1 ]; then
    echo "Each case was run ${REPEAT} times per model. A cell shows how the runs split, then what each run answered."
  fi
  [ -n "${REUSED_NOTE:-}" ] && { echo "$REUSED_NOTE"; echo; }
  if [ -n "${SKIPPED:-}" ]; then
    echo "**Not scored: ${SKIPPED}.** The fault did not show up in the cluster in time (see the \`# verify:\` line of the case), so these say nothing about any model."
    echo
  fi
  HDR="| App | Set | Known cause | Rule-based model |"; SEP="|---|---|---|---|"
  for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue
    IFS='|' read -r label prov model base keyenv <<< "$spec"
    HDR="$HDR $label (\`${model:-$prov}\`) |"; SEP="$SEP---|"
  done
  echo "$HDR"; echo "$SEP"
  while IFS=$'\t' read -r -a F; do
    line="| ${F[0]} | $(row_set "${F[0]}") | ${F[1]} | $(cell "${F[2]}" "${F[1]}") |"
    for ((k=3; k<${#F[@]}; k++)); do line="$line $(cell "${F[$k]}" "${F[1]}") |"; done
    echo "$line"
  done < "$R"
  echo
  local SUM="**Rule-based: $(both_sets 0).**"
  i=1; for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue
    IFS='|' read -r label _ <<< "$spec"; SUM="$SUM **$label: $(both_sets $i).**"; i=$((i+1))
  done
  [ ${#SPECS[@]} -eq 0 ] && SUM="$SUM No model service was tested (set ACC_MODELS or GEMINI_API_KEY)."
  echo "$SUM"
  echo
  echo "The **dev** set is what the prompt and the code were tuned against. The **held-out** set was never used for tuning, so its numbers are the more honest ones."
  echo
  if [ -s "${E:-/nonexistent}" ]; then
    echo "## Explanations"
    echo "A right category is not enough: for every right answer the report is also checked for the terms the case requires (\`# must-mention\`) and for false claims (\`# must-not-claim\`)."
    local ES="**Rule-based: $(explain_summary rules).**"
    i=1; for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue
      IFS='|' read -r label _ <<< "$spec"; ES="$ES **$label: $(explain_summary "$label").**"; i=$((i+1))
    done
    echo "$ES"
    local c col run st
    while IFS=$'\t' read -r c col run st; do
      [ "$st" = ok ] && continue
      [ "$col" = rules ] && col="Rule-based"
      echo "- $col, $c, run $run: $st"
    done < "$E"
    echo
  fi
  if [ -s "${ERRFILE:-/nonexistent}" ]; then
    echo "## Why runs failed"
    echo "A failed run (⛔) is not a wrong answer. **rate_limit**: the service's limit or quota was reached. **network**: the service could not be reached (retried twice before giving up). **auth**: the key was refused. **timeout**: no answer in time. **other**: see the log. A run that was *skipped* was not tried, because the model had just failed 3 times in a row."
    awk '{
      label=$2; if (label=="rules") label="Rule-based"
      if (match($0, /last reason: [a-z_]+\)/))   print label "\t2\t" substr($0, RSTART+13, RLENGTH-14)
      else if (match($0, /: \[[a-z_]+\]/))       print label "\t1\t" substr($0, RSTART+3, RLENGTH-4)
    }' "$ERRFILE" | sort | uniq -c | awk '{
      label=$2; kind=$3; reason=$4; n=$1
      txt = (kind==1) ? n " " reason : n " skipped after " reason
      if (label!=last) { if (last!="") print "- " last ": " line; line=txt; last=label } else line=line "; " txt
    } END { if (last!="") print "- " last ": " line }'
    echo
  fi
  echo "## What this does not show"
  if [ "${REPEAT:-1}" -gt 1 ]; then
    echo "- $N cases is a small sample, not a benchmark. Each case was run ${REPEAT} times per model. Results can differ between runs: AI answers vary, and the rule-based model depends on what the cluster looks like at the moment it is checked."
  else
    echo "- $N cases is a small sample, not a benchmark. One run each. Results can differ between runs: AI answers vary, and the rule-based model depends on what the cluster looks like at the moment it is checked."
  fi
  local sim="" c
  while IFS=$'\t' read -r -a F; do
    c=$(row_case "${F[0]}"); t=$(sed -n 's/^# simulated: *//p' "$CASES_DIR/$c.yaml" 2>/dev/null | head -1)
    [ -n "$t" ] && sim="${sim:+$sim; }$c: $t"
  done < "$R"
  [ -n "$sim" ] && echo "- Simulated or partly simulated: $sim."
  cat <<'TEXT'
- The apps are tiny stand-ins, not real systems. The Kubernetes behaviour in most cases is real (DNS, quotas, access rules, scheduling, volumes, probes, two cases use a real Postgres), but the applications only print a few lines.
- The held-out cases were not used for tuning, but the same author wrote them at the same time as the categories, so they are not independent. Real past incidents are still to be added.
- The rule-based model only knows the simplest causes, so it cannot be right on newer categories. Its column is a baseline, not a competitor.
- `ERROR` can also mean the model service rate-limited the run or it ran out of time. That says nothing about how accurate the model is. A model can also fail by calling a tool that does not exist: the analyzer retries that twice, so an `ERROR` that remains means every try failed.
- The checks on the explanation are keyword checks. They catch a report that never names the evidence, not every weak explanation.
- The cases were written by the author of the tool.
TEXT
}
