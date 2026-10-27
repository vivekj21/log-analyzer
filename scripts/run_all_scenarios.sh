#!/usr/bin/env bash
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOGS_DIR="$REPO_ROOT/testdata/logs"
TEST_REPO="/tmp/log-analyzer-test-repo"

echo "================================================================================"
echo "          Testing Log Analyzer Against 10 Diverse Production Scenarios          "
echo "================================================================================"

rm -rf "$TEST_REPO"
mkdir -p "$TEST_REPO"
git -C "$TEST_REPO" init -b main >/dev/null 2>&1
echo "# Incident Postmortems" > "$TEST_REPO/README.md"
git -C "$TEST_REPO" -c user.name="Tester" -c user.email="test@local" add README.md
git -C "$TEST_REPO" -c user.name="Tester" -c user.email="test@local" commit -m "initial commit" >/dev/null 2>&1

for logfile in "$LOGS_DIR"/*.log; do
    fname=$(basename "$logfile")
    echo ""
    echo "▶ Running scenario: $fname"
    echo "--------------------------------------------------------------------------------"
    go run "$REPO_ROOT/cmd/analyzer/main.go" \
        --log-file="$logfile" \
        --git-repo="$TEST_REPO" \
        --git=true
done

echo ""
echo "================================================================================"
echo "✔ All 10 scenarios processed! Listing incident files committed to Git:"
echo "================================================================================"
ls -lh "$TEST_REPO/incidents"
echo ""
echo "Git commits created:"
git -C "$TEST_REPO" log --oneline -n 10
