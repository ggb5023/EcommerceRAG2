#!/usr/bin/env bash
# Restore the OSS backup into a directory and verify every checksum.
set -euo pipefail
umask 077

ENV_FILE=${ECR_OSS_ENV:-/etc/ecommerce-rag/oss.env}
REPO=${ECR_BACKUP_REPO:-/data/app/EcommerceRAG2}
BUCKET_PREFIX=ecr-backup
DEST=""
PUBLIC=0
RESTORE_BUNDLE=0
REQUIRE_REVISION=0

usage() { echo "usage: $0 --dest DIR [--public-endpoint] [--git-bundle] [--require-revision]" >&2; }
while [ $# -gt 0 ]; do
  case "$1" in
    --dest) DEST=${2:?missing directory}; shift ;;
    --public-endpoint) PUBLIC=1 ;;
    --git-bundle) RESTORE_BUNDLE=1 ;;
    --require-revision) REQUIRE_REVISION=1 ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
  shift
done
[ -n "$DEST" ] || { usage; exit 2; }
fail() { echo "FAIL oss-restore: $*" >&2; exit 1; }
[ -f "$ENV_FILE" ] || fail "credential file missing"
[ "$(stat -c '%a' "$ENV_FILE")" = "600" ] || fail "credential file must be mode 0600"
set -a; . "$ENV_FILE"; set +a
for key in OSS_ACCESS_KEY_ID OSS_ACCESS_KEY_SECRET OSS_ENDPOINT OSS_BUCKET; do
  [ -n "${!key:-}" ] || fail "missing $key in $ENV_FILE"
done
ENDPOINT=$OSS_ENDPOINT
if [ "$PUBLIC" = 1 ]; then ENDPOINT=${OSS_ENDPOINT_PUBLIC:-}; [ -n "$ENDPOINT" ] || fail "public endpoint not configured"; fi
oss() { ossutil "$@" -e "$ENDPOINT" -i "$OSS_ACCESS_KEY_ID" -k "$OSS_ACCESS_KEY_SECRET"; }
BASE="oss://$OSS_BUCKET/$BUCKET_PREFIX"

mkdir -p "$DEST"
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

verify_manifest() { # dir manifest
  local dir=$1 manifest=$2
  grep -v '^#' "$manifest" > "$TMP/check.sha256"
  ( cd "$dir" && sha256sum -c "$TMP/check.sha256" >/dev/null )
  echo "PASS sha256 $manifest"
}

restore_tree() { # prefix manifest_key local_root; 0=restored, 2=present but empty, 1=missing
  local prefix=$1 manifest_key=$2 root=$3
  local manifest=$TMP/manifest.sha256 count=0
  if ! oss cp "$BASE/$manifest_key" "$manifest" -f -q >/dev/null 2>&1 || [ ! -s "$manifest" ]; then
    return 1
  fi
  mkdir -p "$root"
  while IFS= read -r line; do
    case "$line" in \#*|'') continue ;; esac
    name=${line#*  }
    mkdir -p "$root/$(dirname "$name")"
    oss cp "$BASE/$prefix/$name" "$root/$name" -f -q >/dev/null
    count=$((count+1))
  done < "$manifest"
  [ "$count" -gt 0 ] || return 2
  cp "$manifest" "$root/manifest.sha256"
  verify_manifest "$root" "$manifest"
}

CORPUS_STATUS=absent
set +e
restore_tree corpus/revision/current corpus/revision/current/manifest.sha256 "$DEST/corpus/revision/current"
corpus_rc=$?
set -e
case "$corpus_rc" in
  0) CORPUS_STATUS=present; echo "RESTORE corpus revision present" ;;
  2) echo "SKIP corpus revision absent in backup"
     if [ "$REQUIRE_REVISION" = 1 ]; then fail "corpus revision required but absent"; fi ;;
  *) fail "corpus backup manifest missing" ;;
esac

restore_tree eval/current eval/current/manifest.sha256 "$DEST/eval/current" || fail "eval backup missing"

if [ "$REQUIRE_REVISION" = 1 ] || [ "$CORPUS_STATUS" = present ]; then
  ECR_EVAL_REVISION="$DEST/corpus/revision/current" ECR_REQUIRE_EVAL=1 python3 "$REPO/scripts/check/check-eval-corpus.py" || fail "corpus check failed"
else
  echo "SKIP required corpus check: corpus absent"
  ECR_EVAL_REVISION="$DEST/corpus/revision/current" python3 "$REPO/scripts/check/check-eval-corpus.py" || fail "corpus check failed"
fi

if [ "$RESTORE_BUNDLE" = 1 ]; then
  key=$(oss ls "$BASE/git/" -q 2>/dev/null | grep -o 'oss://[^ ]*\.bundle' | tail -1)
  [ -n "$key" ] || fail "no git bundle in backup"
  name=$(basename "$key")
  oss cp "$key" "$DEST/git/$name" -f -q >/dev/null 2>&1 || { mkdir -p "$DEST/git"; oss cp "$key" "$DEST/git/$name" -f -q; }
  git -C "$REPO" bundle verify "$DEST/git/$name" >/dev/null || fail "git bundle verify failed"
  echo "PASS git bundle $name"
fi

echo "SUMMARY restored=$DEST corpus_revision=$CORPUS_STATUS"
echo "PASS oss-restore"
