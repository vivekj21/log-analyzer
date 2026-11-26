#!/usr/bin/env bash
# Downloads six third-party sample logs from Loghub (https://github.com/logpai/loghub) into
# testdata/production_logs/, for the tests that use real-world logs. They are NOT stored in this repository:
# Loghub's terms allow research or academic use, and this project is Apache-2.0. See testdata/README.md.
#
# Run:  scripts/fetch-loghub-samples.sh        (`make test` and `make ci` run it for you)
# Safe to run again: files that are already right are left alone.
# Each file is taken from a fixed Loghub commit, converted to Windows line endings (the tests want
# that on purpose) and checked against a SHA-256 below. A wrong checksum stops the script.
set -u
cd "$(dirname "$0")/.." || exit 1

LOGHUB_COMMIT=dd61d0952749ee7963bde24220d1be5ede023033
DEST=testdata/production_logs
# name here | path inside Loghub | SHA-256 of the converted file
FILES="
real-hdfs-datanode.log|HDFS/HDFS_2k.log|7c967000980c086ed55fa6544ba4f05fe66d44622795e890c68caf8bbb635035
real-linux-syslog.log|Linux/Linux_2k.log|b3e20bc1afe732ab1bf3ed1de4bf9c809e4194e02f7dea911d918e5342e8e173
real-openssh-auth.log|OpenSSH/OpenSSH_2k.log|1e4912727fa88245113d41b16a0cd25ceadba7f931e1c406542885b91254264f
real-openstack.log|OpenStack/OpenStack_2k.log|025a1bc64ff5b2ef4a4bda6c4ad5c5c5f18478b71cd1ad2b0676e01625629f2f
real-spark-cluster.log|Spark/Spark_2k.log|2e8b9a37fc5c238253e0b8e18a8bd5e489671def91767ae1192d28c8e1f95901
real-zookeeper.log|Zookeeper/Zookeeper_2k.log|e40e0af5ef9eb6e4097200f260b9d1f626b3676f861a432e87977242e75543d8
"

fail() { echo "fetch-loghub-samples: $*" >&2; exit 1; }
for t in curl perl; do command -v "$t" >/dev/null || fail "needs '$t' on your PATH"; done
if command -v sha256sum >/dev/null; then sha() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null; then sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else fail "needs sha256sum or shasum on your PATH"; fi

mkdir -p "$DEST"
TMP=$(mktemp -d) || fail "cannot create a temporary folder"
trap 'rm -rf "$TMP"' EXIT

echo "$FILES" | while IFS='|' read -r name src want; do
  [ -n "$name" ] || continue
  if [ -f "$DEST/$name" ] && [ "$(sha "$DEST/$name")" = "$want" ]; then
    echo "  ok       $name (already here)"; continue
  fi
  url="https://raw.githubusercontent.com/logpai/loghub/$LOGHUB_COMMIT/$src"
  curl -fsSL --retry 3 --max-time 120 -o "$TMP/raw" "$url" || fail "download failed: $url"
  perl -pe 's/\r?\n/\r\n/' "$TMP/raw" > "$TMP/conv" || fail "line-ending conversion failed for $name"
  got=$(sha "$TMP/conv")
  [ "$got" = "$want" ] || fail "$name: checksum is $got, expected $want (the source changed, or the download is damaged). Nothing was kept."
  mv "$TMP/conv" "$DEST/$name" || fail "cannot write $DEST/$name"
  echo "  fetched  $name"
done
status=$?
[ "$status" -eq 0 ] || exit "$status"
echo "Loghub samples are in $DEST (terms: $DEST/LOGHUB-NOTICE.txt)"
