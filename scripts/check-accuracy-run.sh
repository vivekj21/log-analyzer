#!/usr/bin/env bash
# Checks accuracy.sh with fake tools (no cluster, no network, no key), using a small made-up set of cases:
#  (1) a model that always fails with a rate limit leaves its logs in ACC_LOGS_DIR, gets the failure reason written to errors.txt,
#      and is not called again after 3 failures in a row;
#  (2) a case whose fault never shows up ("# verify:" fails) is not scored and no model is asked about it;
#  (3) ACC_SET picks only the cases of one set, and a case with "# service: -" is analysed without --service;
#  (4) the explanation checklist is applied to right answers only;
#  (6) a network failure is retried and then counts only if every try fails; every failure is labelled with its reason;
#  (7) answers are kept per commit: a second run asks only for what is missing, and a failed run is never kept;
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
prov=""; repo=""; dry=0; ns=""; all="$*"
while [ $# -gt 0 ]; do case "$1" in --provider) prov=$2;; --git-repo) repo=$2;; --dry-run) dry=1;; --namespace) ns=$2;; esac; shift; done
[ $dry = 1 ] && exit 0
echo "$all" >> "$CALLS"
net="Investigation Failed: [MODEL_ERROR] Post https://x/v1:generateContent: dial tcp: lookup x: no such host"
case "$prov" in
  openai) echo "429 rate limit: tokens per day exceeded" >&2; exit 1;;
  down)   echo "tool result: pod blocked, exceeded quota in namespace" >&2; echo "$net" >&2; exit 1;;
  flaky)  if [ ! -e "$FLAKY_DIR/$ns" ]; then touch "$FLAKY_DIR/$ns"; echo "$net" >&2; exit 1; fi;;
esac
[ "$prov" = nocat ] && { echo "a report without any category" > "$repo/r.md"; exit 0; }
echo '"OOM_KILLED"' > "$repo/r.md"
A
chmod +x "$out"
GO
chmod +x "$T/bin/"*
export CALLS="$T/calls.txt"; : > "$CALLS"; export FLAKY_DIR="$T/flaky"; mkdir "$FLAKY_DIR"
run() { PATH="$T/bin:$PATH" K3D_CLUSTER=x WAIT=0 ACC_PAUSE=0 ACC_VERIFY_WAIT=1 ACC_VERIFY_POLL=1 ACC_CASES_DIR="$T/cases" \
  ACC_LOGS_DIR="$T/logs" ACC_RETRY_WAITS="0 0" ACC_REUSE=0 FAKE_KEY=k "$@" bash scripts/accuracy.sh; }

# (1)(2)(4) full run, 2 runs per case
run env ACC_REPEAT=2 ACC_MODELS="fake|openai|m|http://x|FAKE_KEY" > "$T/out.txt" 2>&1 || { tail -20 "$T/out.txt"; fail "script exited with an error"; }
E=$(ls "$T"/logs/*/errors.txt 2>/dev/null | head -1)
[ -n "$E" ] || fail "no logs kept in ACC_LOGS_DIR"
grep -q "429 rate limit" "$E" || fail "failure reason not recorded"
grep -q '\[rate_limit\]' "$E" || fail "the failure is not labelled rate_limit"
grep -q 'last reason: rate_limit' "$E" || fail "skipped runs do not say why"
grep -q -- '- fake: 3 rate_limit; 7 skipped after rate_limit' "$T/out.txt" || { grep -n 'Why runs failed' -A4 "$T/out.txt"; fail "the scorecard does not group the failures by reason"; }
[ "$(grep -c 'skipped (3 failures in a row' "$E")" = 7 ] || { cat "$E"; fail "model kept being called after 3 failures"; }
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

# (6) network failure: retried, then counted only if every try fails
: > "$CALLS"; rm -rf "$FLAKY_DIR"; mkdir "$FLAKY_DIR"
run env ACC_CASES="a1 a2" ACC_MODELS="flk|flaky|m|http://x|FAKE_KEY" > "$T/out5.txt" 2>&1 || fail "flaky run failed"
grep -q "^| a[12] .*⛔" "$T/out5.txt" && { grep -n "^| a" "$T/out5.txt" | head -3; fail "a network failure that succeeds on retry was counted as failed"; }
[ "$(grep -c -- '--provider flaky' "$CALLS")" = 4 ] || { grep -- '--provider flaky' "$CALLS"; fail "expected 2 cases x (1 failure + 1 retry) = 4 calls"; }
: > "$CALLS"
run env ACC_CASES="a1 a2 a3" ACC_MODELS="dwn|down|m|http://x|FAKE_KEY" > "$T/out6.txt" 2>&1 || fail "down run failed"
[ "$(grep -c -- '--provider down' "$CALLS")" = 9 ] || { grep -c -- '--provider down' "$CALLS"; fail "expected 3 cells x 3 tries = 9 calls before the model is skipped"; }
grep -q -- '- dwn: 3 network$' "$T/out6.txt" || { grep -n 'Why runs' -A4 "$T/out6.txt"; fail "network failures are not labelled network"; }
grep -q 'after 2 retries' "$T/logs/"*/errors.txt || fail "errors.txt does not say how often it retried"

# (7) answers are kept per commit
reuse() { run env ACC_REUSE=1 ACC_FORCE_COMMIT="$1" ACC_CELLS_DIR="$T/cells" ACC_CASES="a1 a2" ACC_RETRY_WAITS="" "${@:2}"; }
: > "$CALLS"; rm -rf "$FLAKY_DIR"; mkdir "$FLAKY_DIR"
reuse c1 ACC_MODELS="flk|flaky|m|http://x|FAKE_KEY" > "$T/out7.txt" 2>&1 || fail "first reuse run failed"
grep -q "^| a[12] .*⛔" "$T/out7.txt" || fail "with no retries the first call of each case should fail"
[ "$(ls "$T/cells" | grep -vc "\.md$")" = 0 ] || { ls -la "$T/cells"; cat "$T/out7.txt" | tail -30; fail "a failed run was kept"; }
: > "$CALLS"
reuse c1 ACC_MODELS="flk|flaky|m|http://x|FAKE_KEY" > "$T/out8.txt" 2>&1 || fail "second reuse run failed"
grep -q "^| a[12] .*⛔" "$T/out8.txt" && fail "the second run should succeed (the fake only fails once per case)"
[ "$(grep -c -- '--provider flaky' "$CALLS")" = 2 ] || fail "second run should ask the model once per case"
: > "$CALLS"
reuse c1 ACC_MODELS="flk|flaky|m|http://x|FAKE_KEY" > "$T/out9.txt" 2>&1 || fail "third reuse run failed"
[ "$(grep -c -- '--provider' "$CALLS")" = 0 ] || fail "third run asked the model again although every answer was kept"
grep -q "Answers reused from earlier runs at this same commit (not asked again): flk 2" "$T/out9.txt" || { grep -n reused "$T/out9.txt"; fail "the scorecard does not say that answers were reused"; }
: > "$CALLS"; reuse c2 ACC_MODELS="flk|flaky|m|http://x|FAKE_KEY" > "$T/out10.txt" 2>&1 || fail "new commit run failed"
[ "$(grep -c -- '--provider flaky' "$CALLS")" -ge 2 ] || fail "a new commit must not reuse answers"
# a report without a category is not kept; reused answers do not cost the pause that slow services get
: > "$CALLS"
reuse c3 ACC_MODELS="nc|nocat|m|http://x|FAKE_KEY" > /dev/null 2>&1 || fail "nocat run failed"
reuse c3 ACC_MODELS="nc|nocat|m|http://x|FAKE_KEY" > /dev/null 2>&1 || fail "nocat second run failed"
[ "$(grep -c -- '--provider nocat' "$CALLS")" = 4 ] || fail "an answer without a category must be asked again, not kept"
START=$SECONDS
reuse c1 ACC_SLOW=flk ACC_PAUSE=5 ACC_MODELS="flk|flaky|m|http://x|FAKE_KEY" > /dev/null 2>&1 || fail "pause run failed"
[ $((SECONDS - START)) -lt 5 ] || fail "answers that were reused still waited for the pause"
echo "check-accuracy-run: OK (logs kept, failure reasons, network retry, answers kept per commit, failing model skipped, invisible faults not scored, sets and service flag, explanation checks, validate-only)"
