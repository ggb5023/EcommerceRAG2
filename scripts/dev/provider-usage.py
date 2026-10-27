#!/usr/bin/env python3
"""Read-only token usage summary from usage_log."""
import argparse
from datetime import UTC, datetime, timedelta
import os, sys
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "python"))
from app.providers.usage import aggregate

p=argparse.ArgumentParser()
p.add_argument("command", choices=["status"], nargs="?", default="status")
p.add_argument("--since", type=datetime.fromisoformat)
p.add_argument("--until", type=datetime.fromisoformat)
p.add_argument("--tenant", type=int)
p.add_argument("--slot")
a=p.parse_args()
dsn=os.environ.get("V2_USAGE_DSN") or os.environ.get("V2_GATEWAY_DSN")
if not dsn: raise SystemExit("V2_USAGE_DSN or V2_GATEWAY_DSN is required")
since=a.since or datetime.now(UTC)-timedelta(days=30)
print(aggregate(dsn, a.tenant, since=since, until=a.until, slot=a.slot))
