#!/usr/bin/env bash
# Runs the real analyzer end to end against a fake model server, once per provider (openai, anthropic).
# No cluster, no real key, no internet. Needs python3 (standard library only). Part of `make ci`.
set -u
cd "$(dirname "$0")/.."
command -v python3 >/dev/null || { echo "check-providers: python3 is needed"; exit 1; }
T=$(mktemp -d); SP=""
trap '[ -n "$SP" ] && kill "$SP" 2>/dev/null; rm -rf "$T"' EXIT
KEY="sk-CANARY-PROVIDER-KEY-0123456789"
printf '%s\n' "$KEY" > "$T/key"; chmod 400 "$T/key"
go build -o "$T/analyzer" ./cmd/analyzer || { echo "CHECK FAILED: build"; exit 1; }

PORT=$((20000 + RANDOM % 10000))
python3 scripts/fake_llm_server.py "$PORT" "$T/req.log" & SP=$!
for _ in $(seq 1 50); do (echo > "/dev/tcp/127.0.0.1/$PORT") 2>/dev/null && break; sleep 0.1; done

FAIL=0
fail() { echo "CHECK FAILED: $*"; FAIL=1; }
for P in openai anthropic; do
  REPO="$T/repo-$P"; mkdir -p "$REPO"
  if ! env -u GEMINI_API_KEY "$T/analyzer" --provider "$P" --base-url "http://127.0.0.1:$PORT/v1" --model fake-model \
        --api-key-file "$T/key" --namespace demo --service web --symptom "crash loop" --dry-run \
        --git-repo "$REPO" --timeout 2m >"$T/out-$P.log" 2>&1; then
    fail "$P: analyzer exited with an error"; tail -8 "$T/out-$P.log"; continue
  fi
  grep -rq --exclude-dir=.git "CRASHLOOP_BAD_CONFIG" "$REPO" || fail "$P: the committed report does not have the model's answer"
  grep -q "Tool invoked: list_pods" "$T/out-$P.log" || fail "$P: the model's tool call was not run"
  if grep -rqF --exclude-dir=.git "$KEY" "$REPO" "$T/out-$P.log"; then fail "$P: the API key leaked into the report or the logs"; fi
done
python3 - "$T/req.log" "$KEY" <<'PY'
import json, sys
seen = {"openai": False, "anthropic": False}
for line in open(sys.argv[1]):
    d = json.loads(line)
    if d["path"].endswith("/chat/completions"):
        assert d["auth"] == "Bearer " + sys.argv[2], "openai: wrong Authorization header"
        assert len(d["body"]["tools"]) > 0 and d["body"]["messages"][0]["role"] == "system", "openai: tools or system prompt missing"
        seen["openai"] = True
    elif d["path"].endswith("/messages"):
        assert d["xkey"] == sys.argv[2], "anthropic: wrong x-api-key header"
        assert "SECURITY RULES" in d["body"]["system"], "anthropic: untrusted-data rules missing from the system prompt"
        seen["anthropic"] = True
assert all(seen.values()), "the fake server did not see both providers: %s" % seen
PY
PYRC=$?
[ "$PYRC" -eq 0 ] || fail "request headers or bodies were wrong (see the assertion above)"

# Refusals: each must stop the program before anything is sent.
expect_refusal() { # $1=label $2=text that must appear; rest = args
  local label=$1 want=$2; shift 2
  if env -u GEMINI_API_KEY "$T/analyzer" "$@" --namespace demo --dry-run --git-repo "$T/refuse" >"$T/refuse.log" 2>&1; then fail "$label: should have been refused"; return; fi
  grep -qF "$want" "$T/refuse.log" || { fail "$label: refused, but without the expected message ($want)"; tail -3 "$T/refuse.log"; }
}
expect_refusal "plain http to another machine" "plain http" --provider openai --model m --base-url http://10.0.0.5:8000/v1 --api-key-file "$T/key"
# A model service that cannot be reached must make the program FAIL (exit code 1), not look like a success.
if env -u GEMINI_API_KEY "$T/analyzer" --provider openai --model m --base-url "http://127.0.0.1:9/v1" --api-key-file "$T/key" --namespace demo --dry-run --git-repo "$T/unreachable" --timeout 40s >"$T/unreachable.log" 2>&1; then
  fail "an unreachable model service must give a non-zero exit code"
fi
grep -q "Investigation Failed" "$T/unreachable.log" || fail "an unreachable model service must print 'Investigation Failed'"

# --allow-http is the explicit way around the http rule: it must get past the check (heuristic mode, so nothing is sent).
if ! env -u GEMINI_API_KEY "$T/analyzer" --llm heuristic --provider openai --model m --base-url http://10.0.0.5:8000/v1 --allow-http --api-key-file "$T/key" --namespace demo --dry-run --git-repo "$T/allow" >"$T/allow.log" 2>&1; then
  fail "--allow-http should let an http address through"; tail -3 "$T/allow.log"
fi
grep -q "WARNING: plain http" "$T/allow.log" || fail "--allow-http must log a warning"
expect_refusal "missing model name" "needs --model" --provider anthropic --api-key-file "$T/key"
expect_refusal "gemini key file with another provider" "only works with --provider=gemini" --provider openai --model m --gemini-key-file "$T/key"
expect_refusal "unknown provider" "unknown --provider" --provider bard --api-key-file "$T/key"

[ "$FAIL" = 0 ] && echo "check-providers: OK (openai and anthropic end to end; key never leaked; unsafe settings refused)"
exit "$FAIL"
