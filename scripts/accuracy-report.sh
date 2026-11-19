#!/usr/bin/env bash
# The scorecard text. Sourced by accuracy.sh (real run) and by check-accuracy-report.sh (test with made-up results).
# Needs these variables: R (results file, tab separated: app, known cause, rule-based answers, then one column per model;
# several runs of one cell are separated by commas), SPECS (array of "label|provider|model|base_url|KEYENV"),
# N (number of cases), REPEAT (runs per case), TOTAL (N * REPEAT). Optional: ACC_CASES (a partial run).
#
# Every run lands in one of four groups:
#   right   the answer equals the known cause
#   unsure  the answer is CANNOT_DETERMINE ("I cannot tell"): not right, but not a wrong diagnosis either
#   wrong   any other category
#   failed  ERROR or NONE: the run produced no answer

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

# Summary for column $1 (0 = rule-based, 1.. = models), counted over every run of every case.
column_summary() {
  local col=$1 right=0 unsure=0 wrong=0 failed=0 line x
  local -a F a
  while IFS=$'\t' read -r -a F; do
    local IFS=','; a=(${F[$((col+2))]}); IFS=$'\t'
    for x in "${a[@]}"; do
      case "$(classify "$x" "${F[1]}")" in right) right=$((right+1));; unsure) unsure=$((unsure+1));; wrong) wrong=$((wrong+1));; failed) failed=$((failed+1));; esac
    done
  done < "$R"
  local extra=""
  [ "$unsure" -gt 0 ] && extra="$unsure unsure"
  [ "$wrong" -gt 0 ] && extra="${extra:+$extra, }$wrong wrong"
  [ "$failed" -gt 0 ] && extra="${extra:+$extra, }$failed failed"
  local unit=""; [ "${REPEAT:-1}" -gt 1 ] && unit=" runs"
  echo "$right of $TOTAL$unit right${extra:+ ($extra)}"
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
  echo
  HDR="| App | Known cause | Rule-based model |"; SEP="|---|---|---|"
  for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue
    IFS='|' read -r label prov model base keyenv <<< "$spec"
    HDR="$HDR $label (\`${model:-$prov}\`) |"; SEP="$SEP---|"
  done
  echo "$HDR"; echo "$SEP"
  while IFS=$'\t' read -r -a F; do
    line="| ${F[0]} | ${F[1]} | $(cell "${F[2]}" "${F[1]}") |"
    for ((k=3; k<${#F[@]}; k++)); do line="$line $(cell "${F[$k]}" "${F[1]}") |"; done
    echo "$line"
  done < "$R"
  echo
  local SUM="**Rule-based: $(column_summary 0).**"
  i=1; for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue
    IFS='|' read -r label _ <<< "$spec"; SUM="$SUM **$label: $(column_summary $i).**"; i=$((i+1))
  done
  [ ${#SPECS[@]} -eq 0 ] && SUM="$SUM No model service was tested (set ACC_MODELS or GEMINI_API_KEY)."
  echo "$SUM"
  echo
  echo "## What this does not show"
  if [ "${REPEAT:-1}" -gt 1 ]; then
    echo "- $N cases is a small sample, not a benchmark. Each case was run ${REPEAT} times per model. Results can differ between runs: AI answers vary, and the rule-based model depends on what the cluster looks like at the moment it is checked."
  else
    echo "- $N cases is a small sample, not a benchmark. One run each. Results can differ between runs: AI answers vary, and the rule-based model depends on what the cluster looks like at the moment it is checked."
  fi
  cat <<'TEXT'
- The cases are simple: one broken app alone in its namespace. Real incidents are messier.
- `dblock` is simulated: the app only prints database-lock errors, there is no real database.
- `ERROR` can also mean the model service rate-limited the run or it ran out of time. That says nothing about how accurate the model is. A model can also fail by calling a tool that does not exist: the analyzer retries that twice, so an `ERROR` that remains means every try failed.
- The cases were written by the author of the tool.
TEXT
}
