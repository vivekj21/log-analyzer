#!/usr/bin/env bash
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOGS_DIR="$REPO_ROOT/testdata/logs"
TARGET_REPO="${1:-$REPO_ROOT}"

echo "================================================================================"
echo "          Testing Log Analyzer Against 10 Diverse Production Scenarios          "
echo "================================================================================"
echo "Target Repository: $TARGET_REPO"

mkdir -p "$TARGET_REPO/incidents"

for logfile in "$LOGS_DIR"/*.log; do
    fname=$(basename "$logfile")
    echo ""
    echo "▶ Running scenario: $fname"
    echo "--------------------------------------------------------------------------------"
    go run "$REPO_ROOT/cmd/analyzer/main.go" \
        --log-file="$logfile" \
        --git-repo="$TARGET_REPO" \
        --branch=false \
        --git=true
done

echo ""
echo "================================================================================"
echo "✔ All 10 scenarios processed! Listing incident files in $TARGET_REPO/incidents:"
echo "================================================================================"
ls -lh "$TARGET_REPO/incidents"
echo ""
echo "Git commits created on current branch:"
git -C "$TARGET_REPO" log --oneline -n 12
