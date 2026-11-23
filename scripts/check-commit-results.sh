#!/usr/bin/env bash
# Checks scripts/commit-results.sh with fake scorecards in a throwaway git repo (no cluster, no network). Run by `make ci`.
set -u
cd "$(dirname "$0")/.."
SRC="$PWD/scripts/commit-results.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT; FAILED=0
fail() { echo "CHECK FAILED: $*"; FAILED=1; }
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t

git init -q --bare "$T/remote.git"
git clone -q "$T/remote.git" "$T/repo" 2>/dev/null
cd "$T/repo" || exit 1
mkdir scripts accuracy
cp "$SRC" scripts/commit-results.sh
printf '#!/bin/sh\nexit 0\n' > fakemake; chmod +x fakemake
printf '#!/bin/sh\nexit 1\n' > failmake; chmod +x failmake
echo old > accuracy/RESULTS.md; echo x > other.txt
git add -A; git commit -q -m init; git push -q origin HEAD 2>/dev/null
BASE=$(git rev-parse HEAD)

try() { # name, expect (commit|refuse), content, [make]
  git reset -q --hard "$BASE"; git clean -fdq -e fakemake -e failmake
  printf '%b' "$3" > accuracy/RESULTS.md
  echo changed > other.txt; git add other.txt          # staged by someone else: must NOT be committed
  out=$(MAKE="./${4:-fakemake}" bash scripts/commit-results.sh 2>&1); rc=$?
  head=$(git rev-parse HEAD)
  if [ "$2" = commit ]; then
    [ "$rc" = 0 ] && [ "$head" != "$BASE" ] || fail "$1: should commit ($out)"
    [ "$(git show --name-only --format= HEAD)" = "accuracy/RESULTS.md" ] || fail "$1: must commit only accuracy/RESULTS.md"
    [ "$(git --git-dir="$T/remote.git" rev-parse "$(git branch --show-current)" 2>/dev/null)" = "$head" ] || fail "$1: not pushed"
  else
    [ "$rc" != 0 ] && [ "$head" = "$BASE" ] || fail "$1: should refuse and commit nothing ($out)"
    grep -q "NOT COMMITTED" <<< "$out" || fail "$1: no clear reason printed"
  fi
}
CLEAN='# Scorecard\n| oom | dev | OOM_KILLED | ✅ 3 right |\nA failed run (⛔) is not a wrong answer.\n'
try clean commit "$CLEAN"
try unchanged refuse "old\n"
try partial refuse "$CLEAN**Partial run: only these cases were run: oom.**\n"
try not-scored refuse "$CLEAN**Not scored: oom.** The fault did not show up\n"
try failed-cell refuse "| oom | dev | OOM_KILLED | ⛔ 3 failed: ERROR, ERROR, ERROR |\n"
try report-check-fails refuse "$CLEAN" failmake
[ "$FAILED" = 0 ] && echo "check-commit-results: OK (clean scorecard commits only RESULTS.md; unchanged, partial, not-scored, failed-cell and failing report check are refused)" || exit 1
