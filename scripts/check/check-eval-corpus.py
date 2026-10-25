"""Check the out-of-repo frozen evaluation corpus without requiring it locally."""
from __future__ import annotations

import hashlib
import json
import os
import sys
from pathlib import Path

DEFAULT_ROOT = Path("/var/lib/ecommerce-rag/eval/synthetic-m2-v1-aligned-provisional")
CASES = "synthetic-m2-v1-revision-1.jsonl"
METADATA = "synthetic-m2-v1-revision-1.metadata.json"
NEEDED = (CASES, METADATA, "documents.jsonl", "alignment-revision-1.json")


def main() -> int:
    root = Path(os.environ.get("ECR_EVAL_REVISION") or DEFAULT_ROOT)
    missing = [name for name in NEEDED if not (root / name).is_file()]
    if missing:
        message = f"SKIP eval corpus absent under {root}: {', '.join(missing)}"
        print(message, file=sys.stderr if os.environ.get("ECR_REQUIRE_EVAL") == "1" else sys.stdout)
        return 1 if os.environ.get("ECR_REQUIRE_EVAL") == "1" else 0
    metadata = json.loads((root / METADATA).read_text())
    actual = hashlib.sha256((root / CASES).read_bytes()).hexdigest()
    if metadata.get("sha256") != actual:
        print(f"FAIL metadata sha256 mismatch for {CASES}", file=sys.stderr)
        return 1
    print(f"PASS eval corpus present; sha256={actual[:12]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
