#!/usr/bin/env bash
# Checks that log-file mode masks secrets and email addresses before anything is written to Git,
# and that --llm heuristic never sends the log to Gemini.
# No cluster and no API key needed. Part of `make ci`.
set -u
cd "$(dirname "$0")/.."
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
cat > "$T/app.log" <<'LOG'
2026-11-15 10:00:01 INFO starting payment-service
2026-11-15 10:00:02 INFO login by jane.doe@example.com
2026-11-15 10:00:03 ERROR db connect failed password=CANARY_SECRET_9f3a1c
2026-11-15 10:00:04 FATAL panic: runtime error: out of memory
LOG
REPO="$T/repo"; mkdir -p "$REPO"
if ! env -u GEMINI_API_KEY go run ./cmd/analyzer --log-file "$T/app.log" --git-repo "$REPO" >"$T/out.log" 2>&1; then
  echo "CHECK FAILED: analyzer did not run"; tail -15 "$T/out.log"; exit 1
fi
FILES=$(grep -rIl --exclude-dir=.git "" "$REPO" 2>/dev/null)
[ -n "$FILES" ] || { echo "CHECK FAILED: no report file was written"; exit 1; }
FAIL=0
for needle in CANARY_SECRET_9f3a1c jane.doe@example.com; do
  if echo "$FILES" | xargs grep -l -F "$needle" >/dev/null 2>&1; then echo "CHECK FAILED: '$needle' reached the Git report"; FAIL=1; fi
done
echo "$FILES" | xargs grep -q -F "[REDACTED]" || { echo "CHECK FAILED: no [REDACTED] marker (was the test log even read?)"; FAIL=1; }
echo "$FILES" | xargs grep -q -F "out of memory" || { echo "CHECK FAILED: report lost the real error text"; FAIL=1; }
# --llm heuristic must never send anything to Gemini, even when a key is present.
env GEMINI_API_KEY=dummy-key-not-real go run ./cmd/analyzer --log-file "$T/app.log" --git-repo "$T/repo2" --llm heuristic >"$T/out2.log" 2>&1
if grep -q "sending this log file" "$T/out2.log"; then echo "CHECK FAILED: --llm heuristic still sent the log file to Gemini"; FAIL=1; fi
[ "$FAIL" = 0 ] && echo "check-redaction: OK (secret and email masked, real error kept, --llm heuristic sends nothing)"
exit "$FAIL"
