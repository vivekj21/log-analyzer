#!/usr/bin/env bash
# Checks the scorecard text with made-up results (no cluster needed). Run by `make ci`.
set -u
cd "$(dirname "$0")/.."
. scripts/accuracy-report.sh
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT; R="$T/results.tsv"; E="$T/explain.tsv"; FAILED=0
fail() { echo "CHECK FAILED: $*"; FAILED=1; }
has() { grep -qF -- "$2" <<< "$1" || fail "$3: missing «$2»"; }
hasnt() { grep -qF -- "$2" <<< "$1" && fail "$3: should not contain «$2»"; }

# a tiny set of cases: two dev (oom, dblock is simulated) and one held-out (silent-crash)
CASES_DIR="$T/cases"; mkdir "$CASES_DIR"
printf '# expect: OOM_KILLED\n# set: dev\n' > "$CASES_DIR/oom.yaml"
printf '# expect: NO_FAULT\n# set: dev\n' > "$CASES_DIR/healthy.yaml"
printf '# expect: DB_LOCK\n# set: dev\n# simulated: only prints lock errors\n' > "$CASES_DIR/dblock.yaml"
printf '# expect: CANNOT_DETERMINE\n# set: heldout\n' > "$CASES_DIR/silent-crash.yaml"

# three runs per case
SPECS=("gemini|gemini|||K" "groq|openai|openai/gpt-oss-120b|http://x|K")
printf 'oom\tOOM_KILLED\tOOM_KILLED,CANNOT_DETERMINE,OOM_KILLED\tOOM_KILLED,OOM_KILLED,OOM_KILLED\tERROR,ERROR,ERROR\nhealthy\tNO_FAULT\tCANNOT_DETERMINE,CANNOT_DETERMINE,CANNOT_DETERMINE\tNO_FAULT,DB_LOCK,NO_FAULT\tNO_FAULT,NO_FAULT,NO_FAULT\ndblock (simulated)\tDB_LOCK\tDB_LOCK,DB_LOCK,DB_LOCK\tDB_LOCK,DB_LOCK,DB_LOCK\tDB_LOCK,DB_LOCK,DB_LOCK\nsilent-crash\tCANNOT_DETERMINE\tOOM_KILLED,OOM_KILLED,OOM_KILLED\tCANNOT_DETERMINE,CANNOT_DETERMINE,OOM_KILLED\tCANNOT_DETERMINE,CANNOT_DETERMINE,CANNOT_DETERMINE\n' > "$R"
printf 'oom\tgemini\t1\tok\noom\tgemini\t2\tmissing: memory\nsilent-crash\tgroq\t1\tclaimed: OOMKilled\nsilent-crash\tgroq\t2\tok\noom\trules\t1\tok\n' > "$E"
N=4; REPEAT=3; TOTAL=12; unset ACC_CASES; SKIPPED=""
OUT=$(write_report 2>&1)
for want in '| oom | dev | OOM_KILLED | ❔ 2 right, 1 unsure' '✅ 3 right: OOM_KILLED, OOM_KILLED, OOM_KILLED' '❔ 2 right, 1 unsure: OOM_KILLED, CANNOT_DETERMINE, OOM_KILLED' '⛔ 3 failed: ERROR, ERROR, ERROR' \
            '❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE' '❌ 2 right, 1 wrong: NO_FAULT, DB_LOCK, NO_FAULT' \
            '| dblock (simulated) | dev |' '| silent-crash | heldout |' \
            '**Rule-based: dev 5 of 9 runs right (4 unsure); held-out 0 of 3 runs right (3 wrong).**' \
            '**gemini: dev 8 of 9 runs right (1 wrong); held-out 2 of 3 runs right (1 wrong).**' \
            '**groq: dev 6 of 9 runs right (3 failed); held-out 3 of 3 runs right.**' \
            'groq (`openai/gpt-oss-120b`)' 'gemini (`gemini`)' '- `ERROR` can also mean the model service rate-limited' \
            '- Simulated or partly simulated: dblock: only prints lock errors.' 'The **held-out** set was never used for tuning' \
            'Each case was run 3 times per model' '`CANNOT_DETERMINE` (❔ unsure)' '## Explanations' '**gemini: 1 of 2 right answers also passed the checklist.**' \
            '**groq: 1 of 2 right answers also passed the checklist.**' '**Rule-based: 1 of 1 right answers also passed the checklist.**' \
            '- gemini, oom, run 2: missing: memory' '- groq, silent-crash, run 1: claimed: OOMKilled' 'not independent'; do
  has "$OUT" "$want" "3 runs"
done
hasnt "$OUT" "Partial run" "3 runs, full"
hasnt "$OUT" "Not scored" "3 runs, nothing skipped"
hasnt "$OUT" "Why runs failed" "3 runs, no error file"
hasnt "$OUT" "reused from earlier" "3 runs, nothing reused"

# one run per case, partial, one case skipped, no explanation checks
SPECS=("gemini|gemini|||K")
printf 'healthy\tNO_FAULT\tCANNOT_DETERMINE\tNO_FAULT\noom\tOOM_KILLED\tDB_LOCK\tERROR\n' > "$R"; : > "$E"
printf 'healthy gemini run 1: [network] (after 2 retries) Investigation Failed: dial tcp\noom gemini run 1: [rate_limit] 429\noom groq run 1: skipped (3 failures in a row; last reason: rate_limit)\nhealthy groq run 1: skipped (3 failures in a row; last reason: rate_limit)\nx rules run 1: [other] boom\n' > "$T/errors.txt"
ERRFILE="$T/errors.txt"; REUSED_NOTE="Answers reused from earlier runs of the same code (not asked again): gemini 3."
N=2; REPEAT=1; TOTAL=2; ACC_CASES="healthy oom"; SKIPPED="silent-crash"
OUT=$(write_report 2>&1)
for want in '| healthy | dev | NO_FAULT | ❔ CANNOT_DETERMINE | ✅ NO_FAULT |' '| oom | dev | OOM_KILLED | ❌ DB_LOCK | ⛔ ERROR |' \
            '**Rule-based: dev 0 of 2 right (1 unsure, 1 wrong).**' '**gemini: dev 1 of 2 right (1 failed).**' 'Partial run: only these cases were run: healthy oom' 'One run each' \
            '**Not scored: silent-crash.**' '## Why runs failed' '- gemini: 1 network; 1 rate_limit' '- groq: 2 skipped after rate_limit' '- Rule-based: 1 other' 'A failed run (⛔) is not a wrong answer.' 'Answers reused from earlier runs of the same code (not asked again): gemini 3.'; do
  has "$OUT" "$want" "1 run"
done
hasnt "$OUT" " runs right" "1 run"
hasnt "$OUT" "## Explanations" "1 run, no checks"
hasnt "$OUT" "command not found" "1 run"
[ "$FAILED" = 0 ] && echo "check-accuracy-report: OK" || exit 1
