#!/usr/bin/env bash
# Checks the permissions the Helm chart asks for, by rendering it (no cluster needed). Part of `make ci`.
# Default: a Role in the investigated namespace only, no cluster-wide role, no secrets, no write verbs.
set -u
cd "$(dirname "$0")/.."
command -v helm >/dev/null || { echo "check-helm-rbac: helm not installed, skipped"; exit 0; }
FAIL=0; fail() { echo "CHECK FAILED: $*"; FAIL=1; }
render() { helm template t deploy/helm/log-analyzer --set gemini.existingSecret=x "$@" 2>&1; }

OUT=$(render --set investigation.namespace=prod)
echo "$OUT" | grep -q "^kind: Role$"        || fail "default: expected a namespaced Role"
echo "$OUT" | grep -q "^kind: RoleBinding$" || fail "default: expected a RoleBinding"
echo "$OUT" | grep -qE "^kind: Cluster(Role|RoleBinding)$" && fail "default: no cluster-wide role is allowed"
[ "$(echo "$OUT" | grep -c "namespace: prod")" -ge 2 ] || fail "default: the Role and RoleBinding must be in the investigated namespace (prod)"
echo "$OUT" | grep -q '"nodes"' && fail "default: nodes must not be readable"
echo "$OUT" | grep -qi '"secrets"' && fail "secrets must never be readable"
echo "$OUT" | grep -qE 'verbs: \[[^]]*"(create|update|patch|delete|deletecollection|\*)"' && fail "only read verbs are allowed"
echo "$OUT" | grep -q '"configmaps"' && fail "default: configmaps must not be readable"

OUT=$(render --set rbac.allowConfigMaps=true)
echo "$OUT" | grep -q '"configmaps"' || fail "allowConfigMaps=true should add configmaps"

OUT=$(render --set rbac.clusterWide=true)
echo "$OUT" | grep -q "^kind: ClusterRole$" || fail "clusterWide=true: expected a ClusterRole"
echo "$OUT" | grep -q '"nodes"'             || fail "clusterWide=true: nodes should be readable"
echo "$OUT" | grep -qi '"secrets"' && fail "clusterWide=true: secrets must never be readable"

OUT=$(render --set rbac.create=false)
echo "$OUT" | grep -qE "^kind: (Cluster)?Role" && fail "rbac.create=false must not create any role"

OUT=$(render)
echo "$OUT" | grep -q "memory: 512Mi" || fail "the Job must set a memory limit"

[ "$FAIL" = 0 ] && echo "check-helm-rbac: OK (namespaced read-only Role by default, cluster-wide only on request, no secrets, no write verbs)" || exit 1
