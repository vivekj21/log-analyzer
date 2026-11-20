#!/usr/bin/env bash
# Checks accuracy.sh with fake tools (no cluster, no network, no key), using a small made-up set of cases:
#  (1) a model that always fails with a rate limit leaves its logs in ACC_LOGS_DIR, gets the failure reason written to errors.txt,
#      and is not called again after 3 failures in a row;
#  (2) a case whose fault never shows up ("# verify:" fails) is not scored and no model is asked about it;
#  (3) ACC_SET picks only the cases of one set, and a case with "# service: -" is analysed without --service;
#  (4) the explanation checklist is applied to right answers only;
#  (5) ACC_VALIDATE_ONLY=1 checks the faults without calling any model and fails if one is not visible.
set -eu
cd "$(dirname "$0")/.."
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir "$T/bin" "$T/cases"
fail() { echo "check-accuracy-run: FAIL ($*)"; exit 1; }
mkcase() { # name set verify must-mention [service] ; EXP and NOT can be set before the call
  { echo "# expect: ${EXP:-OOM_KILLED}"; [ -n "${NOT:-}" ] && echo "# must-not-claim: $NOT"; echo "# symptom: test symptom"; echo "# set: $2"; echo "# verify: $3"; echo "# must-mention: $4"
    [ -n "${5:-}" ] && echo "# service: $5"; echo "kind: Namespace"; echo "metadata: {name: la-acc-$1-extra}"; } > "$T/cases/$1.yaml"
}
mkcase a1 dev true OOM_KILLED
mkcase a2 dev true zzz-never-in-the-report
mkcase a3 heldout true OOM_KILLED -
mkcase nope dev false OOM_KILLED
EXP=NO_FAULT mkcase a4 dev true zzz-never-in-the-report      # the fake model answers OOM_KILLED, which is wrong here: no checklist
NOT=OOM_KILLED mkcase a5 dev true OOM_KILLED                 # the report says OOM_KILLED, which this case forbids
printf '#!/bin/bash\nif [ "$1" = config ] && [ "$2" = current-context ]; then echo k3d-x; fi\nexit 0\n' > "$T/bin/kubectl"
printf '#!/bin/bash\nexit 0\n' > "$T/bin/k3d"
printf '#!/bin/bash\necho k3d-x-server-0\n' > "$T/bin/docker"
cat > "$T/bin/go" <<'GO'
#!/bin/bash
out=""; while [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done
cat > "$out" <<'A'
#!/bin/bash
prov=""; repo=""; dry=0; all="$*"
while [ $# -gt 0 ]; do case "$1" in --provider) prov=$2;; --git-repo) repo=$2;; --dry-run) dry=1;; esac; shift; done
[ $dry = 1 ] && exit 0
echo "$all" >> "$CALLS"
if [ "$prov" = openai ]; then echo "429 rate limit: tokens per day exceeded" >&2; exit 1; fi
echo '"OOM_KILLED"' > "$repo/r.md"
A
chmod +x "$out"
GO
chmod +x "$T/bin/"*
export CALLS="$T/calls.txt"; : > "$CALLS"
run() { PATH="$T/bin:$PATH" K3D_CLUSTER=x WAIT=0 ACC_PAUSE=0 ACC_VERIFY_WAIT=1 ACC_VERIFY_POLL=1 ACC_CASES_DIR="$T/cases" \
  ACC_LOGS_DIR="$T/logs" FAKE_KEY=k "$@" bash scripts/accuracy.sh; }

# (1)(2)(4) full run, 2 runs per case
run env ACC_REPEAT=2 ACC_MODELS="fake|openai|m|http://x|FAKE_KEY" > "$T/out.txt" 2>&1 || { tail -20 "$T/out.txt"; fail "script exited with an error"; }
E=$(ls "$T"/logs/*/errors.txt 2>/dev/null | head -1)
[ -n "$E" ] || fail "no logs kept in ACC_LOGS_DIR"
grep -q "429 rate limit" "$E" || fail "failure reason not recorded"
[ "$(grep -c 'skipped (3 failures in a row)' "$E")" = 7 ] || { cat "$E"; fail "model kept being called after 3 failures"; }
grep -q "NOT VISIBLE   : nope" "$T/out.txt" || fail "the case without a visible fault was not reported"
grep -q "Not scored: nope" "$T/out.txt" || fail "the scorecard does not say that nope was not scored"
grep -q "la-acc-nope" "$CALLS" && fail "a model was asked about a case whose fault never showed up"
grep -q "| nope |" "$T/out.txt" && fail "the unscored case is in the table"
grep -q "Partial run" "$T/out.txt" || fail "a run with an unscored case must be marked partial"
grep -q -- "- Rule-based, a2, run 1: missing: zzz-never-in-the-report" "$T/out.txt" || { grep -n "Explanations" -A6 "$T/out.txt"; fail "explanation check did not flag a2"; }
grep -q -- "- Rule-based, a1" "$T/out.txt" && fail "a1 passed the checklist and must not be listed"
grep -q -- "- Rule-based, a4" "$T/out.txt" && fail "a wrong answer must not get an explanation check"
grep -q -- "- Rule-based, a5, run 1: claimed: OOM_KILLED" "$T/out.txt" || fail "must-not-claim was not applied to a5"

# (3) only the held-out set; a3 has "# service: -"
: > "$CALLS"
run env ACC_SET=heldout > "$T/out2.txt" 2>&1 || { tail -20 "$T/out2.txt"; fail "ACC_SET=heldout run failed"; }
grep -q "la-acc-a3" "$CALLS" || fail "ACC_SET=heldout did not run a3"
grep -q "la-acc-a1\|la-acc-a2" "$CALLS" && fail "ACC_SET=heldout ran a dev case"
grep "la-acc-a3" "$CALLS" | grep -q -- "--service" && fail "a case with '# service: -' was given --service"
: > "$CALLS"
run env ACC_CASES="a1" > "$T/out3.txt" 2>&1 || fail "a1 only run failed"
grep "la-acc-a1" "$CALLS" | grep -q -- "--service a1" || fail "a case without '# service:' must default to --service <case name>"

# (5) validate only
: > "$CALLS"
if run env ACC_VALIDATE_ONLY=1 > "$T/out4.txt" 2>&1; then fail "validate-only must fail while a fault is not visible"; fi
grep -q "fault visible : a1" "$T/out4.txt" || fail "validate-only did not list a1 as visible"
grep -q "NOT VISIBLE   : nope" "$T/out4.txt" || fail "validate-only did not list nope"
[ ! -s "$CALLS" ] || fail "validate-only called the analyzer"
echo "check-accuracy-run: OK (logs kept, failure reason shown, failing model skipped, invisible faults not scored, sets and service flag, explanation checks, validate-only)"
