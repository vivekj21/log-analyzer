#!/usr/bin/env bash
# Checks the scorecard text with made-up results (no cluster needed). Run by `make ci`.
set -u
cd "$(dirname "$0")/.."
. scripts/accuracy-report.sh
T=$(mktemp -d); R="$T/results.tsv"; FAILED=0
fail() { echo "CHECK FAILED: $*"; FAILED=1; }
has() { grep -qF -- "$2" <<< "$1" || fail "$3: missing «$2»"; }
hasnt() { grep -qF -- "$2" <<< "$1" && fail "$3: should not contain «$2»"; }

# three runs per case
SPECS=("gemini|gemini|||K" "groq|openai|openai/gpt-oss-120b|http://x|K")
printf 'oom\tOOM_KILLED\tOOM_KILLED,CANNOT_DETERMINE,OOM_KILLED\tOOM_KILLED,OOM_KILLED,OOM_KILLED\tERROR,ERROR,ERROR\nhealthy\tNO_FAULT\tCANNOT_DETERMINE,CANNOT_DETERMINE,CANNOT_DETERMINE\tNO_FAULT,DB_LOCK,NO_FAULT\tNO_FAULT,NO_FAULT,NO_FAULT\n' > "$R"
N=2; REPEAT=3; TOTAL=6; unset ACC_CASES
OUT=$(write_report 2>&1)
for want in '✅ 3 right: OOM_KILLED, OOM_KILLED, OOM_KILLED' '❔ 2 right, 1 unsure: OOM_KILLED, CANNOT_DETERMINE, OOM_KILLED' '⛔ 3 failed: ERROR, ERROR, ERROR' \
            '❔ 3 unsure: CANNOT_DETERMINE, CANNOT_DETERMINE, CANNOT_DETERMINE' '❌ 2 right, 1 wrong: NO_FAULT, DB_LOCK, NO_FAULT' \
            '**Rule-based: 2 of 6 runs right (4 unsure).**' '**gemini: 5 of 6 runs right (1 wrong).**' '**groq: 3 of 6 runs right (3 failed).**' \
            'groq (`openai/gpt-oss-120b`)' 'gemini (`gemini`)' '- `ERROR` can also mean the model service rate-limited' '- `dblock` is simulated' \
            'Each case was run 3 times per model' '`CANNOT_DETERMINE` (❔ unsure)'; do
  has "$OUT" "$want" "3 runs"
done
hasnt "$OUT" "Partial run" "3 runs, full"

# one run per case, partial
SPECS=("gemini|gemini|||K")
printf 'healthy\tNO_FAULT\tCANNOT_DETERMINE\tNO_FAULT\noom\tOOM_KILLED\tDB_LOCK\tERROR\n' > "$R"
N=2; REPEAT=1; TOTAL=2; ACC_CASES="healthy oom"
OUT=$(write_report 2>&1)
for want in '| healthy | NO_FAULT | ❔ CANNOT_DETERMINE | ✅ NO_FAULT |' '| oom | OOM_KILLED | ❌ DB_LOCK | ⛔ ERROR |' \
            '**Rule-based: 0 of 2 right (1 unsure, 1 wrong).**' '**gemini: 1 of 2 right (1 failed).**' 'Partial run: only these cases were run: healthy oom' 'One run each'; do
  has "$OUT" "$want" "1 run"
done
hasnt "$OUT" " runs " "1 run"
hasnt "$OUT" "command not found" "1 run"
[ "$FAILED" = 0 ] && echo "check-accuracy-report: OK" || exit 1
