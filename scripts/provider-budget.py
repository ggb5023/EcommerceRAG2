#!/usr/bin/env python3
"""Explicitly initialize or inspect a shared, protected online budget window."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "python"))
from app.providers.budget import BudgetLedger, initialize_ledger
from app.providers.contracts import ProviderError


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("initialize", "status"))
    parser.add_argument("--ledger", type=Path, required=True)
    parser.add_argument("--window-id", required=True)
    parser.add_argument("--cap-cny", required=True)
    args = parser.parse_args()
    try:
        if args.action == "initialize":
            initialize_ledger(args.ledger, args.window_id, args.cap_cny)
        print(json.dumps(BudgetLedger(args.ledger, args.window_id, args.cap_cny).summary()))
        return 0
    except (ProviderError, OSError):
        print("FAIL shared Provider budget (configuration or ledger)", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
