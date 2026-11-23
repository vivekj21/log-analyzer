#!/usr/bin/env bash
# Commits and pushes accuracy/RESULTS.md after a full accuracy run. Nothing else is committed.
# It refuses when the scorecard is not a clean, full one:
#   - RESULTS.md did not change since the last commit
#   - the run was partial ("Partial run") or a case was not scored ("Not scored")
#   - any table cell shows a failed run (⛔)
# MAKE and RESULTS_FILE can be set (used by the CI check).
set -u
cd "$(dirname "$0")/.."
F="${RESULTS_FILE:-accuracy/RESULTS.md}"
refuse() { echo "NOT COMMITTED: $*"; exit 1; }

[ -f "$F" ] || refuse "$F does not exist. Run: make accuracy"
git diff --quiet HEAD -- "$F" 2>/dev/null && refuse "$F is the same as the last commit. Run a full: make accuracy"
grep -qF "Partial run" "$F" && refuse "the scorecard is from a partial run. Run a full: make accuracy"
grep -qF "Not scored" "$F" && refuse "some cases were not scored (their fault did not show up). Run again: make accuracy"
grep -E '^\|.*⛔' "$F" >/dev/null && refuse "some runs failed (⛔ in the table). Run again: make accuracy (answers that worked are reused)"
${MAKE:-make} check-accuracy-report || refuse "make check-accuracy-report failed"

git add -- "$F" || refuse "git add failed"
git commit -q -m "Accuracy scorecard: full run $(date +%Y-%m-%d)" -- "$F" || refuse "git commit failed"
git push -q || { echo "Committed, but git push failed. Fix the connection and run: git push"; exit 1; }
echo "Committed and pushed $F: $(git log --oneline -1)"
