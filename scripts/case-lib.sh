#!/usr/bin/env bash
# Helpers for the accuracy cases. Sourced by accuracy.sh and check-cases.sh.
#
# Every case file in accuracy/cases/ has a "# verify:" line: a shell condition that is true once the fault is
# really visible in the cluster. Inside it you may use these helpers ($NS is the case's namespace, and every
# pod of a case carries the label app=<name>):
#   logs_have APP REGEX      the logs (current or previous container) of APP match REGEX
#   reasons_have APP REGEX   the pod phase or container state/last-state reason of APP matches REGEX (e.g. OOMKilled)
#   has_event REGEX          an event in the namespace matches REGEX
#   phase APP                pod phase (Running, Pending ...)
#   restarts APP             highest container restart count
#   k ...                    kubectl in the case's namespace

k() { kubectl -n "$NS" "$@"; }
has_event() { k get events --no-headers 2>/dev/null | grep -Eiq -- "$1"; }
logs_have() {
  { k logs -l "app=$1" --all-containers --tail=3000 2>/dev/null
    k logs -l "app=$1" --all-containers --tail=3000 --previous 2>/dev/null; } | grep -Eiq -- "$2"
}
reasons() {
  k get pods -l "app=$1" -o jsonpath='{range .items[*]}{.status.phase} {.status.containerStatuses[*].state.waiting.reason} {.status.containerStatuses[*].state.terminated.reason} {.status.containerStatuses[*].lastState.terminated.reason}{"\n"}{end}' 2>/dev/null
}
reasons_have() { reasons "$1" | grep -Eq -- "$2"; }
phase() { k get pods -l "app=$1" -o jsonpath='{.items[0].status.phase}' 2>/dev/null; }
restarts() {
  k get pods -l "app=$1" -o jsonpath='{range .items[*]}{.status.containerStatuses[*].restartCount}{"\n"}{end}' 2>/dev/null | sort -n | tail -1
}

# case_meta NAME KEY  -> first "# KEY: value" line of the case file
case_meta() { sed -n "s/^# $2: *//p" "${CASES_DIR:-accuracy/cases}/$1.yaml" | head -1; }

# verify_case NAME [SECONDS]: 0 = the fault is visible, 1 = not visible (after trying for SECONDS), 2 = no verify line.
# SECONDS 0 means "look once".
verify_case() {
  local cmd end
  cmd=$(case_meta "$1" verify)
  [ -n "$cmd" ] || return 2
  end=$((SECONDS + ${2:-${ACC_VERIFY_WAIT:-180}}))
  while :; do
    ( NS="la-acc-$1"; eval "$cmd" ) >/dev/null 2>&1 && return 0
    [ "$SECONDS" -ge "$end" ] && return 1
    sleep "${ACC_VERIFY_POLL:-5}"
  done
}

# explain_check NAME REPORT_FILE -> "ok", "missing: <regex>" or "claimed: <regex>"
# The case's "# must-mention:" lines must all match the report; no "# must-not-claim:" line may match it.
explain_check() {
  local f="${CASES_DIR:-accuracy/cases}/$1.yaml" re
  while IFS= read -r re; do
    [ -n "$re" ] || continue
    grep -Eiq -- "$re" "$2" || { echo "missing: $re"; return; }
  done < <(sed -n 's/^# must-mention: *//p' "$f")
  while IFS= read -r re; do
    [ -n "$re" ] || continue
    ! grep -Eiq -- "$re" "$2" || { echo "claimed: $re"; return; }
  done < <(sed -n 's/^# must-not-claim: *//p' "$f")
  echo ok
}

# extra_namespaces NAME -> names of additional namespaces the case file creates (for cleanup)
extra_namespaces() { sed -n '/^kind: Namespace/{n;s/.*name: *\([a-z0-9-]*\).*/\1/p}' "${CASES_DIR:-accuracy/cases}/$1.yaml"; }
