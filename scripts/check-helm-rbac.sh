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

# Web service mode (server.enabled): one namespaced Role per listed namespace, a token Secret mounted as a file, still read-only.
SRV="--set server.enabled=true"
if helm template t deploy/helm/log-analyzer $SRV >/dev/null 2>&1; then fail "server: starting without server.namespaces (or clusterWide) must be refused"; fi
OUT=$(render $SRV --set 'server.namespaces={shop,payments}')
[ "$(echo "$OUT" | grep -c "^kind: Role$")" = 2 ]        || fail "server: expected one Role for each listed namespace"
[ "$(echo "$OUT" | grep -c "^kind: RoleBinding$")" = 2 ] || fail "server: expected one RoleBinding for each listed namespace"
echo "$OUT" | grep -qE "^kind: Cluster(Role|RoleBinding)$" && fail "server: no cluster-wide role is allowed by default"
echo "$OUT" | grep -q "^kind: Deployment$"  || fail "server: expected a Deployment"
echo "$OUT" | grep -q "^kind: Service$"     || fail "server: expected a Service"
echo "$OUT" | grep -q "^kind: Job$"         && fail "server: the one-off Job must not be created next to the service"
echo "$OUT" | grep -q -- '--namespaces=shop,payments' || fail "server: the allowed namespaces must reach the program"
echo "$OUT" | grep -q -- '--token-file=/var/run/secrets/la-token/token' || fail "server: the token must be read from a mounted file"
echo "$OUT" | grep -qE "name: LOG_ANALYZER_TOKEN$" && fail "server: the token must not be an environment variable"
echo "$OUT" | grep -qi '"secrets"' && fail "server: secrets must never be readable"
echo "$OUT" | grep -qE 'verbs: \[[^]]*"(create|update|patch|delete|deletecollection|\*)"' && fail "server: only read verbs are allowed"
echo "$OUT" | grep -q "readOnlyRootFilesystem: true" || fail "server: the root filesystem must be read-only"
echo "$OUT" | grep -q "runAsNonRoot: true" || fail "server: must not run as root"
echo "$OUT" | grep -q "path: /healthz" || fail "server: expected a health probe"
echo "$OUT" | grep -q "memory: 512Mi" || fail "server: must set a memory limit"
echo "$OUT" | grep -q "name: t-token" || fail "server: expected the generated token Secret"
OUT=$(render $SRV --set 'server.namespaces={shop}' --set server.tokenSecret=mine)
echo "$OUT" | grep -q "^kind: Secret$" && fail "server: with server.tokenSecret the chart must not create a Secret"
echo "$OUT" | grep -q "secretName: mine" || fail "server: server.tokenSecret must be the mounted Secret"
OUT=$(render $SRV --set rbac.clusterWide=true)
echo "$OUT" | grep -q "^kind: ClusterRole$" || fail "server: clusterWide=true should give a ClusterRole"

[ "$FAIL" = 0 ] && echo "check-helm-rbac: OK (namespaced read-only Role by default, cluster-wide only on request, no secrets, no write verbs; same for the web service)" || exit 1
