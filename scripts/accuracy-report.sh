#!/usr/bin/env bash
# The scorecard text. Sourced by accuracy.sh (real run) and by check-accuracy-report.sh (test with made-up results).
# Needs these variables: R (results file, tab separated), SPECS (array of "label|provider|model|base_url|KEYENV"),
# N (number of cases), REPEAT (runs per case), TOTAL (N * REPEAT), HEUR_OK (right runs of the rule-based model),
# OK (array: right runs for each entry of SPECS). Optional: ACC_CASES (a partial run).

# One table cell. $1 = answers of all runs, comma separated; $2 = the known cause.
cell() {
  local IFS=','; local -a a=($1); local x right=0 n=${#a[@]} mark
  for x in "${a[@]}"; do [ "$x" = "$2" ] && right=$((right+1)); done
  if [ "$n" -eq 1 ]; then
    if [ "$right" -eq 1 ]; then echo "✅ $1"; else echo "❌ $1"; fi
    return
  fi
  mark="⚠️"; [ "$right" -eq "$n" ] && mark="✅"; [ "$right" -eq 0 ] && mark="❌"
  echo "$mark $right of $n: ${1//,/, }"
}

write_report() {
  local label prov model base keyenv HDR SEP line k i unit=""
  [ "${REPEAT:-1}" -gt 1 ] && unit=" runs"
  echo "# Accuracy"
  echo
  if [ -n "${ACC_CASES:-}" ]; then
    echo "**Partial run: only these cases were run: ${ACC_CASES}. This is not the full scorecard.**"
    echo
  fi
  echo "Run on $(date -u +%F) at commit \`$(git rev-parse --short HEAD 2>/dev/null || echo unknown)\`. Each case is a small broken app on a throwaway k3d cluster with a known cause."
  echo "A run is right only if the answer's category equals the known cause. \`ERROR\`/\`NONE\` mean the run failed, and count as wrong."
  if [ "${REPEAT:-1}" -gt 1 ]; then
    echo "Each case was run ${REPEAT} times per model. A cell shows how many runs were right, then what each run answered."
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
  local SUM="**Rule-based: $HEUR_OK of $TOTAL$unit.**"
  i=0; for spec in "${SPECS[@]:-}"; do [ -n "$spec" ] || continue
    IFS='|' read -r label _ <<< "$spec"; SUM="$SUM **$label: ${OK[$i]} of $TOTAL$unit.**"; i=$((i+1))
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
- `ERROR` can also mean the model service rate-limited the run or it ran out of time. That says nothing about how accurate the model is.
- The cases were written by the author of the tool.
TEXT
}
