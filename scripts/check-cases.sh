#!/usr/bin/env bash
# Static checks of the accuracy cases (no cluster, no network). Run by `make ci`.
# It catches the mistakes that would otherwise cost a long, paid accuracy run:
#  - a case with a missing or invalid header, an unknown category, or a bad regular expression
#  - a "# verify:" line with a syntax error or a misspelled helper
#  - a manifest that touches anything outside its own namespaces (cluster-wide objects, other cases' namespaces)
#  - the category list in the Go code, the prompt and accuracy/README.md disagreeing
#  - a category with no case at all, or no held-out case
set -u
cd "$(dirname "$0")/.."
. scripts/case-lib.sh
DIR="${ACC_CASES_DIR:-accuracy/cases}"; CASES_DIR="$DIR"
FAILED=0
fail() { echo "CHECK FAILED: $*"; FAILED=1; }

# 1. one list of categories everywhere
CODE=$(grep -oE 'Category[A-Za-z]+ += "[A-Z_]+"' internal/llm/model.go | grep -oE '"[A-Z_]+"' | tr -d '"' | sort)
[ -n "$CODE" ] || fail "no categories found in internal/llm/model.go"
PROMPT=$(grep '^ROOT_CAUSE_CATEGORY: \[One of:' internal/llm/gemini.go | sed 's/.*One of: *//; s/\].*//' | tr ',' '\n' | tr -d ' ' | sort)
[ "$CODE" = "$PROMPT" ] || fail "categories in internal/llm/model.go and in the prompt (internal/llm/gemini.go) differ: $(diff <(echo "$CODE") <(echo "$PROMPT") | tr '\n' ' ')"
for c in $CODE; do grep -q "\`$c\`" accuracy/README.md || fail "accuracy/README.md does not describe $c"; done

# 2. every case
n=0; dev=0; held=0; ALLEXPECT=""
for f in "$DIR"/*.yaml; do
  c=$(basename "$f" .yaml); n=$((n+1))
  exp=$(case_meta "$c" expect); set=$(case_meta "$c" set); sym=$(case_meta "$c" symptom); ver=$(case_meta "$c" verify)
  echo "$CODE" | grep -qx "$exp" || fail "$c: '# expect: $exp' is not a known category"
  ALLEXPECT="$ALLEXPECT $exp:$set"
  case "$set" in dev) dev=$((dev+1));; heldout) held=$((held+1));; *) fail "$c: '# set:' must be dev or heldout";; esac
  [ -n "$sym" ] || fail "$c: no '# symptom:' line"
  [ -n "$ver" ] || fail "$c: no '# verify:' line"
  [ "$(grep -c '^# expect:' "$f")" = 1 ] && [ "$(grep -c '^# set:' "$f")" = 1 ] && [ "$(grep -c '^# verify:' "$f")" = 1 ] || fail "$c: '# expect:', '# set:' and '# verify:' must each appear exactly once"
  grep -q '^# must-mention:' "$f" || fail "$c: needs at least one '# must-mention:' line (what a good report must name)"
  # the verify line must be valid bash and only use known helpers (kubectl is faked so nothing runs for real)
  bash -n <<< "$ver" 2>/dev/null || fail "$c: the '# verify:' line is not valid shell"
  out=$( ( kubectl() { :; }; NS="la-acc-$c"; eval "$ver" ) 2>&1 >/dev/null ) || true
  grep -q "command not found" <<< "$out" && fail "$c: the '# verify:' line uses an unknown command: $(grep 'command not found' <<< "$out" | head -1)"
  # every regular expression must be valid (grep exits 2 on a bad one)
  while IFS= read -r re; do
    [ -n "$re" ] || continue
    grep -E -- "$re" /dev/null >/dev/null 2>&1; [ $? -le 1 ] || fail "$c: bad regular expression: $re"
  done < <(sed -n 's/^# \(must-mention\|must-not-claim\): *//p' "$f"; echo "$ver" | grep -oE "(logs_have|reasons_have) [^ ]+ '[^']*'" | sed "s/.*'\(.*\)'/\1/"; echo "$ver" | grep -oE "has_event '[^']*'" | sed "s/.*'\(.*\)'/\1/")
  # manifests stay inside their own namespaces
  grep -qE '^\s*(kind: *(ClusterRole|ClusterRoleBinding|Node|PersistentVolume|StorageClass|CustomResourceDefinition|MutatingWebhookConfiguration|ValidatingWebhookConfiguration|PriorityClass)\b)' "$f" && fail "$c: a cluster-wide object is not allowed in a case"
  grep -q "namespace: la-acc-$c\b" "$f" || fail "$c: no object is in the namespace la-acc-$c"
  while IFS= read -r ns; do
    case "$ns" in "la-acc-$c"|"la-acc-$c"-*) ;; *) fail "$c: namespace '$ns' is outside la-acc-$c";; esac
  done < <(grep -oE 'namespace: *[a-z0-9-]+' "$f" | sed 's/namespace: *//' | sort -u; extra_namespaces "$c")
  [ -z "$(grep -n 'la-acc-' "$f" | grep -v "la-acc-$c" | head -1)" ] || fail "$c: mentions another case's namespace"
done

# 3. coverage
for c in $CODE; do grep -q " $c:" <<< " $ALLEXPECT" || fail "category $c has no case"; done
[ "$held" -ge 1 ] || fail "no held-out case"
[ "$FAILED" = 0 ] && echo "check-cases: OK ($n cases: $dev dev, $held held-out; $(echo "$CODE" | wc -l) categories, each with at least one case)" || exit 1
