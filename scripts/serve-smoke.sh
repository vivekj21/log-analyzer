#!/usr/bin/env bash
# Starts a real `log-analyzer serve` and runs the curl line from docs/API.md against it, so the
# documented first command can never stop working without CI noticing.
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
pid=""
trap '[ -n "$pid" ] && kill "$pid" 2>/dev/null || true; rm -rf "$tmp"' EXIT

go build -o "$tmp/la" ./cmd/analyzer
# An empty environment: no API key, so the built-in rules are used and nothing is sent anywhere.
env -i PATH="$PATH" HOME="$tmp" "$tmp/la" serve --dry-run --listen 127.0.0.1:0 2>"$tmp/err" &
pid=$!

base=""
for _ in $(seq 1 100); do
  base=$(sed -n 's/.*serve is ready: \(http:[^ ]*\).*/\1/p' "$tmp/err" | head -1)
  [ -n "$base" ] && break
  sleep 0.2
done
[ -n "$base" ] || { cat "$tmp/err"; echo "serve smoke test: the server did not start"; exit 1; }

hostport=${base#http://}

# 1. the command a person types (the first block in docs/API.md)
cli=$(sed -n '/<!-- quickstart -->/,/<!-- \/quickstart -->/p' docs/API.md | grep '^log-analyzer ' | head -1)
[ -n "$cli" ] || { echo "serve smoke test: no quickstart command in docs/API.md"; exit 1; }
cli=${cli//127.0.0.1:8080/$hostport}
cli=${cli/log-analyzer /\"$tmp/la\" }
out=$(eval "$cli" 2>/dev/null)
echo "$out" | grep -q '^# Incident report:' || { echo "serve smoke test: unexpected reply to the quickstart command: $out"; exit 1; }

# 2. the same thing over HTTP (the second block)
cmd=$(sed -n '/<!-- curl -->/,/<!-- \/curl -->/p' docs/API.md | grep '^curl ' | head -1)
[ -n "$cmd" ] || { echo "serve smoke test: no curl line in docs/API.md"; exit 1; }
cmd=${cmd//http:\/\/127.0.0.1:8080/$base}
cmd=${cmd/curl /curl -sS }
out=$(eval "$cmd")
echo "$out" | grep -q '"category"' && echo "$out" | grep -q '"evidence_check"' || { echo "serve smoke test: unexpected reply: $out"; exit 1; }
curl -fsS "$base/healthz" >/dev/null
curl -fsS "$base/" | grep -q 'log-analyzer --server' || { echo "serve smoke test: the home page does not show the first command"; exit 1; }
echo "serve smoke test: passed"
