"""JSON log formatting shared by the Python ai process.

Only allowlisted metadata is added. Message bodies are produced by callers and
must never contain customer text, prompts, tokens, credentials or DSNs.
"""
from __future__ import annotations

import json
import logging
from datetime import UTC, datetime


class JsonFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        entry = {
            "ts": datetime.fromtimestamp(record.created, UTC).isoformat(),
            "level": record.levelname.lower(),
            "component": getattr(record, "component", record.name),
            "msg": record.getMessage(),
        }
        request_id = getattr(record, "request_id", None)
        if request_id:
            entry["request_id"] = str(request_id)
        return json.dumps(entry, ensure_ascii=False, separators=(",", ":"))


def configure(level: str = "INFO") -> None:
    handler = logging.StreamHandler()
    handler.setFormatter(JsonFormatter())
    root = logging.getLogger()
    root.handlers[:] = [handler]
    root.setLevel(level)
