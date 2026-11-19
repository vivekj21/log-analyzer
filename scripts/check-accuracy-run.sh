#!/usr/bin/env bash
# Checks accuracy.sh with fake tools (no cluster, no network, no key): a model that always fails with a rate limit
# must (1) leave its logs in ACC_LOGS_DIR, (2) get the failure reason written to errors.txt,
# (3) not be called again after 3 failures in a row.
set -eu
cd "$(dirname "$0")/.."
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir "$T/bin"
printf '#!/bin/bash\nif [ "$1" = config ] && [ "$2" = current-context ]; then echo k3d-x; fi\nexit 0\n' > "$T/bin/kubectl"
printf '#!/bin/bash\nexit 0\n' > "$T/bin/k3d"
printf '#!/bin/bash\necho k3d-x-server-0\n' > "$T/bin/docker"
cat > "$T/bin/go" <<'GO'
#!/bin/bash
out=""; while [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done
cat > "$out" <<'A'
#!/bin/bash
prov=""; repo=""; dry=0
while [ $# -gt 0 ]; do case "$1" in --provider) prov=$2;; --git-repo) repo=$2;; --dry-run) dry=1;; esac; shift; done
[ $dry = 1 ] && exit 0
if [ "$prov" = openai ]; then echo "429 rate limit: tokens per day exceeded" >&2; exit 1; fi
echo '"OOM_KILLED"' > "$repo/r.md"
A
chmod +x "$out"
GO
chmod +x "$T/bin/"*
PATH="$T/bin:$PATH" K3D_CLUSTER=x WAIT=0 ACC_PAUSE=0 ACC_CASES="image oom readiness" ACC_REPEAT=2 \
  ACC_LOGS_DIR="$T/logs" FAKE_KEY=k ACC_MODELS="fake|openai|m|http://x|FAKE_KEY" \
  bash scripts/accuracy.sh > "$T/out.txt" 2>&1 || { tail -20 "$T/out.txt"; echo "check-accuracy-run: FAIL (script exited with an error)"; exit 1; }
E=$(ls "$T"/logs/*/errors.txt 2>/dev/null | head -1)
[ -n "$E" ] || { echo "check-accuracy-run: FAIL (no logs kept in ACC_LOGS_DIR)"; exit 1; }
grep -q "429 rate limit" "$E" || { echo "check-accuracy-run: FAIL (failure reason not recorded)"; exit 1; }
[ "$(grep -c 'skipped (3 failures in a row)' "$E")" = 3 ] || { echo "check-accuracy-run: FAIL (model kept being called after 3 failures)"; cat "$E"; exit 1; }
echo "check-accuracy-run: OK (logs kept, failure reason shown, failing model skipped after 3 in a row)"
