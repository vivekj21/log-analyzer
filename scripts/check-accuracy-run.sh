#!/usr/bin/env bash
# Checks accuracy.sh with fake tools (no cluster, no network, no key), using a small made-up set of cases:
#  (1) a model that always fails with a rate limit leaves its logs in ACC_LOGS_DIR, gets the failure reason written to errors.txt,
#      and is not called again after 3 failures in a row;
#  (2) a case whose fault never shows up ("# verify:" fails) is not scored and no model is asked about it;
#  (3) ACC_SET picks only the cases of one set, and a case with "# service: -" is analysed without --service;
#  (4) the explanation checklist is applied to right answers only;
#  (6) a network failure is retried and then counts only if every try fails; every failure is labelled with its reason;
#  (7) answers are kept per version of the Go code (also in a real git checkout: scripts and docs may change, the Go code may not): a second run asks only for what is missing, and a failed run is never kept;
#  (9) ACC_PARALLEL gives the same table as a serial run, really runs cases at the same time, and the timing lines are printed;
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
  slow)   sleep 2;;
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
reuse() { run env ACC_REUSE=1 ACC_FORCE_CODE="$1" ACC_CELLS_DIR="$T/cells" ACC_CASES="a1 a2" ACC_RETRY_WAITS="" "${@:2}"; }
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
grep -q "Answers reused from earlier runs of the same code (not asked again): flk 2" "$T/out9.txt" || { grep -n reused "$T/out9.txt"; fail "the scorecard does not say that answers were reused"; }
: > "$CALLS"; reuse c2 ACC_MODELS="flk|flaky|m|http://x|FAKE_KEY" > "$T/out10.txt" 2>&1 || fail "new commit run failed"
[ "$(grep -c -- '--provider flaky' "$CALLS")" -ge 2 ] || fail "a new commit must not reuse answers"
# a report without a category is not kept; reused answers do not cost the pause that slow services get
: > "$CALLS"
reuse c3 ACC_MODELS="nc|nocat|m|http://x|FAKE_KEY" > /dev/null 2>&1 || fail "nocat run failed"
reuse c3 ACC_MODELS="nc|nocat|m|http://x|FAKE_KEY" > /dev/null 2>&1 || fail "nocat second run failed"
[ "$(grep -c -- "--provider nocat" "$CALLS")" = 4 ] || { echo "calls: $(grep -c -- "--provider nocat" "$CALLS")"; cat "$CALLS" | cut -c1-150; fail "an answer without a category must be asked again, not kept"; }
START=$SECONDS
reuse c1 ACC_SLOW=flk ACC_PAUSE=5 ACC_MODELS="flk|flaky|m|http://x|FAKE_KEY" > /dev/null 2>&1 || fail "pause run failed"
[ $((SECONDS - START)) -lt 5 ] || fail "answers that were reused still waited for the pause"

# (8) the same in a real git checkout: a change to scripts or docs keeps the answers, a change to the Go code starts fresh
G="$T/gitrepo"; mkdir -p "$G/scripts" "$G/internal/llm" "$G/cmd" "$G/docs"
cp scripts/accuracy.sh scripts/accuracy-report.sh scripts/case-lib.sh "$G/scripts/"; cp internal/llm/model.go "$G/internal/llm/"
echo "package main" > "$G/cmd/main.go"; echo "module x" > "$G/go.mod"; : > "$G/go.sum"; echo hi > "$G/docs/a.md"
( cd "$G" && git init -q . && git config user.email t@t && git config user.name t && git add -A && git commit -qm first )
grun() { PATH="$T/bin:$PATH" K3D_CLUSTER=x WAIT=0 ACC_PAUSE=0 ACC_VERIFY_WAIT=1 ACC_VERIFY_POLL=1 ACC_CASES_DIR="$T/cases" ACC_LOGS_DIR="$T/glogs" \
  ACC_RETRY_WAITS="" FAKE_KEY=k ACC_CASES="a1" ACC_MODELS="flk|flaky|m|http://x|FAKE_KEY" bash "$G/scripts/accuracy.sh" > "$T/gout.txt" 2>&1; }
asked() { grep -c -- '--provider flaky' "$CALLS"; }
rm -rf "$FLAKY_DIR"; mkdir "$FLAKY_DIR"
: > "$CALLS"; grun || fail "git run 1"; grun || fail "git run 2"       # run 1 fails once (nothing kept), run 2 succeeds and keeps the answer
: > "$CALLS"; grun || fail "git run 3"; [ "$(asked)" = 0 ] || fail "answers were not reused in a git checkout"
( cd "$G" && echo more >> docs/a.md && echo "# x" >> scripts/accuracy-report.sh && git commit -qam "docs and scripts only" )
: > "$CALLS"; grun || fail "git run 4"; [ "$(asked)" = 0 ] || fail "a change to docs and scripts must keep the answers"
( cd "$G" && echo "// changed" >> internal/llm/model.go && git commit -qam "go code" )
: > "$CALLS"; rm -rf "$FLAKY_DIR"; mkdir "$FLAKY_DIR"; grun || fail "git run 5"; [ "$(asked)" -ge 1 ] || fail "a change to the Go code must not reuse answers"
( cd "$G" && echo "// uncommitted" >> internal/llm/model.go )
: > "$CALLS"; grun || fail "git run 6"; grep -q "Go code has uncommitted changes" "$T/gout.txt" || fail "an uncommitted change to the Go code must switch reuse off and say so"

# (9) parallel cases: same table, really concurrent, plan and timing printed
ALL5="a1 a2 a3 a4 a5"
run env ACC_CASES="$ALL5" ACC_PARALLEL=1 ACC_MODELS="ok|oksvc|m|http://x|FAKE_KEY" > "$T/p1.txt" 2>&1 || fail "serial run failed"
run env ACC_CASES="$ALL5" ACC_PARALLEL=3 ACC_MODELS="ok|oksvc|m|http://x|FAKE_KEY" > "$T/p3.txt" 2>&1 || fail "parallel run failed"
[ "$(grep '^| a' "$T/p1.txt" | cut -d" " -f2 | tr "\n" " ")" = "a1 a2 a3 a4 a5 " ] || fail "the table rows must be in case order (a1 a2 a3 a4 a5)"
diff <(grep '^| a' "$T/p1.txt") <(grep '^| a' "$T/p3.txt") > /dev/null || { diff <(grep '^| a' "$T/p1.txt") <(grep '^| a' "$T/p3.txt"); fail "the parallel table differs from the serial one (rows must keep the case order)"; }
START=$SECONDS
run env ACC_CASES="$ALL5" ACC_PARALLEL=5 ACC_MODELS="slw|slow|m|http://x|FAKE_KEY" > "$T/p5.txt" 2>&1 || fail "slow parallel run failed"
[ $((SECONDS - START)) -lt 8 ] || fail "5 analyses of 2 s with ACC_PARALLEL=5 took $((SECONDS - START)) s: they did not run at the same time"
grep -Eq "^   slw: 5 analyses, median [23]s, longest [23]s" "$T/p5.txt" || { grep -n timing -A3 "$T/p5.txt"; fail "timing summary missing"; }
grep -q "== the whole run took" "$T/p5.txt" || fail "total time missing"
grep -q "== plan: slw: 5 analyses to ask (0 kept); no timing history yet" "$T/p5.txt" || { grep -n "plan" "$T/p5.txt"; fail "first run should say there is no timing history"; }
run env ACC_CASES="$ALL5" ACC_PARALLEL=5 ACC_MODELS="slw|slow|m|http://x|FAKE_KEY" > "$T/p6.txt" 2>&1 || fail "second slow run failed"
grep -Eq "== plan: slw: 5 analyses to ask \(0 kept\), about [0-9]+ min at the usual [23]s each, 5 at a time" "$T/p6.txt" || { grep -n "plan" "$T/p6.txt"; fail "the plan does not use the earlier timings"; }
# a failing service under parallel runs: every case still gets a row, failures are labelled
run env ACC_CASES="$ALL5" ACC_PARALLEL=3 ACC_MODELS="fake|openai|m|http://x|FAKE_KEY" > "$T/p7.txt" 2>&1 || fail "parallel run with a failing service failed"
[ "$(grep -c '^| a' "$T/p7.txt")" = 5 ] || fail "every case needs a row even when the service fails"
[ "$(grep -h '\[rate_limit\]' "$(ls -td "$T"/logs/*/ | head -1)errors.txt" | wc -l)" -ge 3 ] || fail "failures were not labelled rate_limit in a parallel run"
if run env ACC_PARALLEL=0 > "$T/p8.txt" 2>&1; then fail "ACC_PARALLEL=0 must be refused"; fi
grep -q "ACC_PARALLEL must be a whole number" "$T/p8.txt" || fail "no message for a bad ACC_PARALLEL"
echo "check-accuracy-run: OK (logs kept, failure reasons, network retry, answers kept per version of the code, parallel cases and timing, failing model skipped, invisible faults not scored, sets and service flag, explanation checks, validate-only)"
