#!/usr/bin/env bash
# Checks the scorecard text with made-up results (no cluster needed). Run by `make ci`.
set -u
cd "$(dirname "$0")/.."
. scripts/accuracy-report.sh
T=$(mktemp -d); R="$T/results.tsv"; FAILED=0
fail() { echo "CHECK FAILED: $*"; FAILED=1; }
has() { grep -qF -- "$2" <<< "$1" || fail "$3: missing «$2»"; }
hasnt() { grep -qF -- "$2" <<< "$1" && fail "$3: should not contain «$2»"; }

SPECS=("gemini|gemini|||K" "groq|openai|openai/gpt-oss-120b|http://x|K")

# three runs per case
printf 'oom\tOOM_KILLED\tOOM_KILLED,CANNOT_DETERMINE,OOM_KILLED\tOOM_KILLED,OOM_KILLED,OOM_KILLED\tERROR,ERROR,ERROR\nhealthy\tNO_FAULT\tNO_FAULT,NO_FAULT,NO_FAULT\tNO_FAULT,DB_LOCK,NO_FAULT\tNO_FAULT,NO_FAULT,NO_FAULT\n' > "$R"
N=2; REPEAT=3; TOTAL=6; HEUR_OK=5; OK=(5 3); unset ACC_CASES
OUT=$(write_report 2>&1)
for want in '✅ 3 of 3: OOM_KILLED, OOM_KILLED, OOM_KILLED' '⚠️ 2 of 3: OOM_KILLED, CANNOT_DETERMINE, OOM_KILLED' '❌ 0 of 3: ERROR, ERROR, ERROR' \
            '**Rule-based: 5 of 6 runs.**' '**gemini: 5 of 6 runs.**' '**groq: 3 of 6 runs.**' 'groq (`openai/gpt-oss-120b`)' 'gemini (`gemini`)' \
            '- `ERROR` can also mean the model service rate-limited' '- `dblock` is simulated' 'Each case was run 3 times per model'; do
  has "$OUT" "$want" "3 runs"
done
hasnt "$OUT" "Partial run" "3 runs, full"

# one run per case, partial
printf 'healthy\tNO_FAULT\tCANNOT_DETERMINE\tNO_FAULT\n' > "$R"
N=1; REPEAT=1; TOTAL=1; HEUR_OK=0; OK=(1 0); ACC_CASES=healthy
SPECS=("gemini|gemini|||K")
OUT=$(write_report 2>&1)
for want in '| healthy | NO_FAULT | ❌ CANNOT_DETERMINE | ✅ NO_FAULT |' '**Rule-based: 0 of 1.**' '**gemini: 1 of 1.**' 'Partial run: only these cases were run: healthy' 'One run each'; do
  has "$OUT" "$want" "1 run"
done
hasnt "$OUT" " runs." "1 run"
hasnt "$OUT" "command not found" "1 run"
[ "$FAILED" = 0 ] && echo "check-accuracy-report: OK" || exit 1
