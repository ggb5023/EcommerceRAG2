#!/usr/bin/env bash
# Back up the frozen evaluation inputs, eval tree and git history to OSS.
# Secrets stay in /etc/ecommerce-rag/oss.env and are never printed or uploaded.
set -euo pipefail
umask 077

ENV_FILE=${ECR_OSS_ENV:-/etc/ecommerce-rag/oss.env}
REPO=${ECR_BACKUP_REPO:-/data/app/EcommerceRAG2}
BUCKET_PREFIX=ecr-backup
REVISION_DIR=${ECR_EVAL_REVISION:-/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional}
DRY_RUN=0
PUBLIC=0
REQUIRE_REVISION=0
REPORTS_DIR=""

usage() { echo "usage: $0 [--dry-run] [--public-endpoint] [--require-revision] [--with-reports DIR]" >&2; }
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    --public-endpoint) PUBLIC=1 ;;
    --require-revision) REQUIRE_REVISION=1 ;;
    --with-reports) REPORTS_DIR=${2:?missing directory}; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
  shift
done

fail() { echo "FAIL oss-backup: $*" >&2; exit 1; }
[ -f "$ENV_FILE" ] || fail "credential file missing"
[ "$(stat -c '%a' "$ENV_FILE")" = "600" ] || fail "credential file must be mode 0600"
set -a
# shellcheck source=/dev/null
. "$ENV_FILE"
set +a
for key in OSS_ACCESS_KEY_ID OSS_ACCESS_KEY_SECRET OSS_ENDPOINT OSS_BUCKET; do
  [ -n "${!key:-}" ] || fail "missing $key in $ENV_FILE"
done
ENDPOINT=$OSS_ENDPOINT
if [ "$PUBLIC" = 1 ]; then ENDPOINT=${OSS_ENDPOINT_PUBLIC:-}; [ -n "$ENDPOINT" ] || fail "public endpoint not configured"; fi

oss() { ossutil "$@" -e "$ENDPOINT" -i "$OSS_ACCESS_KEY_ID" -k "$OSS_ACCESS_KEY_SECRET"; }

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
WORK=$TMP/work; mkdir -p "$WORK/corpus" "$WORK/eval"
BASE="oss://$OSS_BUCKET/$BUCKET_PREFIX"
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
SHORT=$(git -C "$REPO" rev-parse --short HEAD)

manifest_for() { # dir out
  ( cd "$1" && find . -type f \
      ! -path '*/__pycache__/*' ! -path '*/.pytest_cache/*' ! -name '*.pyc' \
      -printf '%P\0' | sort -z | xargs -0 -r sha256sum ) > "$2"
}

total_bytes=0
uploaded=0
skipped=0
upload_keys=()

upload() { # local key
  local local_file=$1 key=$2
  local size; size=$(stat -c '%s' "$local_file")
  if [ "$DRY_RUN" = 1 ]; then
    echo "PLAN upload $key ($size bytes)"; total_bytes=$((total_bytes+size)); return
  fi
  oss cp "$local_file" "$BASE/$key" -f -q
  local check="$TMP/check.$$"; oss cp "$BASE/$key" "$check" -f -q
  local remote_hash local_hash
  remote_hash=$(sha256sum "$check" | cut -d' ' -f1)
  local_hash=$(sha256sum "$local_file" | cut -d' ' -f1)
  [ "$remote_hash" = "$local_hash" ] || fail "read-back mismatch for $key"
  rm -f "$check"
  uploaded=$((uploaded+1)); total_bytes=$((total_bytes+size)); upload_keys+=("$key $local_hash $size")
  echo "UPLOAD $key $local_hash $size"
}

remote_manifest() { # key dest; returns 1 when absent
  oss cp "$BASE/$1" "$2" -f -q >/dev/null 2>&1 || return 1
  [ -s "$2" ]
}

# ---- corpus revision (may be absent) ----
REV_STATUS=absent
CORPUS_MANIFEST=$WORK/corpus-list.sha256
if [ -d "$REVISION_DIR" ] && [ -n "$(find "$REVISION_DIR" -maxdepth 1 -type f -print -quit)" ]; then
  { echo "# generated_at=$STAMP corpus_revision=present git=$SHORT";
    manifest_for "$REVISION_DIR" /dev/stdout; } > "$CORPUS_MANIFEST"
  REV_STATUS=present
else
  echo "# generated_at=$STAMP corpus_revision=absent git=$SHORT" > "$CORPUS_MANIFEST"
  if [ "$REQUIRE_REVISION" = 1 ]; then fail "corpus revision dir absent: $REVISION_DIR"; fi
  echo "SKIP corpus revision absent: $REVISION_DIR"
fi

corpus_changed=1
if remote_manifest corpus/revision/current/manifest.sha256 "$TMP/remote-corpus.sha256"; then
  if diff -q <(grep -v '^#' "$TMP/remote-corpus.sha256") <(grep -v '^#' "$CORPUS_MANIFEST") >/dev/null; then corpus_changed=0; fi
fi
if [ "$corpus_changed" = 1 ]; then
  cp "$CORPUS_MANIFEST" "$WORK/corpus/manifest.sha256"
  if [ "$REV_STATUS" = present ]; then
    while IFS= read -r line; do
      case "$line" in \#*|'') continue ;; esac
      name=${line#*  }
      cp "$REVISION_DIR/$name" "$WORK/corpus/$name"
      upload "$WORK/corpus/$name" "corpus/revision/current/$name"
    done < "$CORPUS_MANIFEST"
  fi
  upload "$WORK/corpus/manifest.sha256" "corpus/revision/current/manifest.sha256"
  cp "$CORPUS_MANIFEST" "$WORK/snapshot.sha256"
  upload "$WORK/snapshot.sha256" "corpus/revision/snapshots/$STAMP.sha256"
else
  echo "NOOP corpus revision unchanged"
  skipped=$((skipped+1))
fi

# ---- eval tree ----
EVAL_MANIFEST=$WORK/eval-list.sha256
{ echo "# generated_at=$STAMP git=$SHORT corpus_revision=$REV_STATUS";
  manifest_for "$REPO/eval" /dev/stdout; } > "$EVAL_MANIFEST"
eval_changed=1
if remote_manifest eval/current/manifest.sha256 "$TMP/remote-eval.sha256"; then
  if diff -q <(grep -v '^#' "$TMP/remote-eval.sha256") <(grep -v '^#' "$EVAL_MANIFEST") >/dev/null; then eval_changed=0; fi
fi
if [ "$eval_changed" = 1 ]; then
  while IFS= read -r line; do
    case "$line" in \#*|'') continue ;; esac
    name=${line#*  }
    mkdir -p "$WORK/eval/$(dirname "$name")"
    cp "$REPO/eval/$name" "$WORK/eval/$name"
    upload "$WORK/eval/$name" "eval/current/$name"
  done < "$EVAL_MANIFEST"
  cp "$EVAL_MANIFEST" "$WORK/eval-manifest.sha256"
  upload "$WORK/eval-manifest.sha256" "eval/current/manifest.sha256"
else
  echo "NOOP eval unchanged"
  skipped=$((skipped+1))
fi

# ---- git bundle ----
BUNDLE=$WORK/repo-$SHORT.bundle
git -C "$REPO" bundle create "$BUNDLE" --all >/dev/null
git -C "$REPO" bundle verify "$BUNDLE" >/dev/null
BUNDLE_HASH=$(sha256sum "$BUNDLE" | cut -d' ' -f1)
bundle_key="git/$SHORT.bundle"
if remote_manifest "$bundle_key" "$TMP/remote.bundle" && [ "$(sha256sum "$TMP/remote.bundle" | cut -d' ' -f1)" = "$BUNDLE_HASH" ]; then
  echo "NOOP git bundle unchanged: $bundle_key"
  skipped=$((skipped+1))
else
  upload "$BUNDLE" "$bundle_key"
fi

# ---- optional reports ----
if [ -n "$REPORTS_DIR" ]; then
  [ -d "$REPORTS_DIR" ] || fail "reports directory missing"
  while IFS= read -r -d '' file; do
    name=${file#"$REPORTS_DIR"/}
    upload "$file" "reports/$STAMP/$name"
  done < <(find "$REPORTS_DIR" -type f -print0 | sort -z)
fi

if [ "$DRY_RUN" = 0 ]; then
  oss api get-bucket-info --bucket "$OSS_BUCKET" >/dev/null || fail "get-bucket-info failed"
  oss api get-bucket-versioning --bucket "$OSS_BUCKET" >/dev/null || fail "get-bucket-versioning failed"
  oss api get-bucket-encryption --bucket "$OSS_BUCKET" >/dev/null || fail "get-bucket-encryption failed"
fi

echo "SUMMARY corpus_revision=$REV_STATUS git=$SHORT uploaded=$uploaded skipped=$skipped bytes=$total_bytes"
if [ ${#upload_keys[@]} -gt 0 ]; then for entry in "${upload_keys[@]}"; do echo "OBJECT $entry"; done; fi
echo "PASS oss-backup"
