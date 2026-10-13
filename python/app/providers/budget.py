"""Durable, shared reservations for every paid Provider attempt.

Initialization is an explicit operator action. Runtime never recreates a lost
ledger, refunds unknown attempts, or resets the authorized window on restart.
"""
from __future__ import annotations

import hashlib
import json
import os
import sqlite3
import stat
from contextlib import contextmanager
from decimal import Decimal, InvalidOperation
from pathlib import Path
from uuid import uuid4

from .contracts import ProviderError

PRICE_VERSION = "bailian-cn-beijing-20261012"
# RMB per million tokens; highest published tier, no discounts/free quota.
PRICES = {
    "qwen3.8-flash": (Decimal("0.8"), Decimal("2.7")),
    "qwen3.7-plus": (Decimal(6), Decimal(24)),
    "qwen3.7-text-embedding": (Decimal("0.5"), Decimal(0)),
    "qwen3.7-text-rerank": (Decimal("0.5"), Decimal(0)),
}
MICRO_CNY = Decimal(1_000_000)
SCHEMA = """
CREATE TABLE window(id TEXT PRIMARY KEY, cap_micro INTEGER NOT NULL, pricing TEXT NOT NULL);
CREATE TABLE attempt(
 id TEXT PRIMARY KEY, slot TEXT NOT NULL, model TEXT NOT NULL,
 input_sha256 TEXT NOT NULL, input_bound INTEGER NOT NULL, output_bound INTEGER NOT NULL,
 reserved_micro INTEGER NOT NULL CHECK(reserved_micro>0),
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 state TEXT NOT NULL DEFAULT 'reserved', request_id_sha256 TEXT,
 usage_json TEXT, observed_micro INTEGER, http_status INTEGER, error_code TEXT
);
"""


def _blocked() -> ProviderError:
    return ProviderError("budget_blocked", "shared online budget ledger is unavailable or inconsistent")


def _amount(value: str) -> int:
    try:
        amount = Decimal(value) * MICRO_CNY
        if not amount.is_finite() or amount <= 0 or amount != amount.to_integral_value():
            raise ValueError
        return int(amount)
    except (ValueError, InvalidOperation):
        raise _blocked() from None


def _secure_path(path: Path) -> None:
    if not path.is_absolute() or path.parent.resolve() != path.parent:
        raise _blocked()
    parent = path.parent.stat()
    if parent.st_uid != os.geteuid() or stat.S_IMODE(parent.st_mode) & 0o077:
        raise _blocked()
    info = path.lstat()
    if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
            or stat.S_IMODE(info.st_mode) != 0o600 or info.st_nlink != 1):
        raise _blocked()


def initialize_ledger(path: Path, window_id: str, cap_cny: str) -> None:
    """Create once; an existing window can never be overwritten by this API."""
    if not window_id or len(window_id) > 128:
        raise _blocked()
    cap = _amount(cap_cny)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    os.close(fd)
    _secure_path(path)
    with sqlite3.connect(path) as conn:
        conn.execute("PRAGMA synchronous=FULL")
        conn.executescript(SCHEMA)
        conn.execute("INSERT INTO window VALUES (?,?,?)", (window_id, cap, PRICE_VERSION))
    # A crash during initialization leaves an invalid ledger, never a fresh cap.
    directory_fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory_fd)
    finally:
        os.close(directory_fd)


class BudgetLedger:
    def __init__(self, path: Path, window_id: str, cap_cny: str) -> None:
        self.path = path
        self.window_id = window_id
        self.cap_micro = _amount(cap_cny)
        with self._transaction() as conn:
            self._verify(conn)

    @classmethod
    def from_environment(cls) -> BudgetLedger:
        try:
            return cls(Path(os.environ["V2_ONLINE_BUDGET_LEDGER"]),
                       os.environ["V2_ONLINE_BUDGET_ID"], os.environ["V2_ONLINE_BUDGET_CNY"])
        except (KeyError, OSError):
            raise _blocked() from None

    @contextmanager
    def _transaction(self):
        conn = None
        try:
            _secure_path(self.path)
            # mode=rw prevents silent reinitialization after deletion.
            conn = sqlite3.connect(self.path.as_uri() + "?mode=rw", uri=True, timeout=10)
            conn.execute("PRAGMA synchronous=FULL")
            conn.execute("BEGIN IMMEDIATE")
            yield conn
            conn.commit()
        except (OSError, sqlite3.Error, ValueError, TypeError):
            if conn:
                conn.rollback()
            raise _blocked() from None
        finally:
            if conn:
                conn.close()

    def _verify(self, conn) -> None:
        if conn.execute("SELECT id,cap_micro,pricing FROM window").fetchall() != [
            (self.window_id, self.cap_micro, PRICE_VERSION)
        ]:
            raise _blocked()

    def reserve(self, slot: str, model: str, payload: dict, *, region: str, quota_rpm: int | None = None) -> str:
        if region != "cn-beijing" or model not in PRICES:
            raise ProviderError("budget_blocked", "model pricing requires review", slot=slot)
        body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode()
        if len(body) > 65_536:
            raise ProviderError("invalid_input", "paid request exceeds bounded input", slot=slot)
        output_bound = 0
        # UTF-8 bytes exceed normal tokenizer counts. Include a 2x margin plus
        # framing/special-token allowances rather than relying on heuristics.
        input_bound = len(body) * 2 + 8192
        if slot in {"control", "generation"}:
            output_bound = payload["max_tokens"]
        elif slot == "embedding":
            texts = payload["input"]["texts"]
            if len(texts) > 20 or any(len(t.encode()) > 8192 for t in texts):
                raise ProviderError("invalid_input", "embedding batch exceeds bounded input", slot=slot)
            input_bound += 4096 * len(texts)
        elif slot == "rerank":
            data = payload["input"]
            if len(data["documents"]) > 20:
                raise ProviderError("invalid_input", "rerank batch exceeds bounded input", slot=slot)
            input_bound += (len(data["query"].encode()) * 2 + 4096) * len(data["documents"])
        else:
            raise _blocked()
        input_price, output_price = PRICES[model]
        # RMB/million tokens == micro-RMB/token; always round upwards.
        cost = int((input_bound * input_price + output_bound * output_price).to_integral_value(rounding="ROUND_CEILING"))
        identifier = uuid4().hex
        with self._transaction() as conn:
            self._verify(conn)
            if quota_rpm is not None:
                count = conn.execute("SELECT count(*) FROM attempt WHERE julianday(created_at) > julianday('now','-60 seconds')").fetchone()[0]
                if count >= quota_rpm:
                    raise ProviderError("local_rate_limited", "shared request quota is full", slot=slot)
            spent = conn.execute("SELECT coalesce(sum(reserved_micro),0) FROM attempt").fetchone()[0]
            if spent + cost > self.cap_micro:
                raise ProviderError("budget_exhausted", "shared online budget is exhausted", slot=slot)
            conn.execute("INSERT INTO attempt(id,slot,model,input_sha256,input_bound,output_bound,reserved_micro) VALUES (?,?,?,?,?,?,?)",
                         (identifier, slot, model, hashlib.sha256(body).hexdigest(), input_bound, output_bound, cost))
        return identifier

    def quota_delay(self, quota_rpm: int) -> float:
        """Shared wall-clock quota, independent of synthetic business dates."""
        with self._transaction() as conn:
            self._verify(conn)
            count, delay = conn.execute("SELECT count(*), 60-(julianday('now')-min(julianday(created_at)))*86400 "
                                        "FROM attempt WHERE julianday(created_at)>julianday('now','-60 seconds')").fetchone()
        return max(.01, float(delay)) if count >= quota_rpm else 0.0

    def extend_authorized_cap(self, cap_cny: str, *, authorization: str) -> None:
        """Explicit operator action; retain every request and record the change."""
        new_cap = _amount(cap_cny)
        if (new_cap <= self.cap_micro or not authorization or len(authorization) > 128
                or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_." for c in authorization)):
            raise _blocked()
        with self._transaction() as conn:
            self._verify(conn)
            conn.execute("CREATE TABLE IF NOT EXISTS cap_amendment(authorization TEXT PRIMARY KEY,"
                         "old_cap_micro INTEGER NOT NULL,new_cap_micro INTEGER NOT NULL,"
                         "created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))")
            conn.execute("INSERT INTO cap_amendment(authorization,old_cap_micro,new_cap_micro) VALUES(?,?,?)",
                         (authorization, self.cap_micro, new_cap))
            conn.execute("UPDATE window SET cap_micro=? WHERE id=?", (new_cap, self.window_id))
        self.cap_micro = new_cap

    def record(self, identifier: str, *, data: dict | None = None,
               http_status: int | None = None, error_code: str | None = None) -> None:
        """Keep the full reservation even after errors or missing usage."""
        usage = None
        request_hash = None
        observed = None
        data = data or {}
        request_id = data.get("request_id", data.get("id"))
        if isinstance(request_id, str):
            request_hash = hashlib.sha256(request_id.encode()).hexdigest()
        raw_usage = data.get("usage")
        if isinstance(raw_usage, dict):
            usage = {key: raw_usage[key] for key in (
                "input_tokens", "output_tokens", "total_tokens", "prompt_tokens", "completion_tokens", "tokens"
            ) if isinstance(raw_usage.get(key), int) and not isinstance(raw_usage[key], bool) and raw_usage[key] >= 0}
        with self._transaction() as conn:
            self._verify(conn)
            row = conn.execute("SELECT model,input_bound,output_bound FROM attempt WHERE id=?", (identifier,)).fetchone()
            if row is None:
                raise _blocked()
            if usage:
                inputs = usage.get("input_tokens", usage.get("prompt_tokens", usage.get("total_tokens", usage.get("tokens"))))
                outputs = usage.get("output_tokens", usage.get("completion_tokens", 0 if row[2] == 0 else None))
                if inputs is not None and outputs is not None:
                    if inputs > row[1] or outputs > row[2]:
                        # A violated bound stops all subsequent requests until
                        # operator review; do not silently trust the estimate.
                        conn.execute("UPDATE window SET pricing='BOUND_VIOLATION'")
                        error_code = "budget_bound_violation"
                    rates = PRICES[row[0]]
                    observed = int((inputs * rates[0] + outputs * rates[1]).to_integral_value(rounding="ROUND_CEILING"))
            conn.execute("UPDATE attempt SET state=?,request_id_sha256=?,usage_json=?,observed_micro=?,http_status=?,error_code=? WHERE id=?",
                         ("error" if error_code else "response", request_hash, json.dumps(usage) if usage else None,
                          observed, http_status, error_code, identifier))
        if error_code == "budget_bound_violation":
            raise _blocked()

    def summary(self) -> dict:
        with self._transaction() as conn:
            self._verify(conn)
            count, reserved = conn.execute("SELECT count(*),coalesce(sum(reserved_micro),0) FROM attempt").fetchone()
        return {"window_id": self.window_id, "requests": count,
                "cap_cny": str(Decimal(self.cap_micro) / MICRO_CNY),
                "reserved_cny": str(Decimal(reserved) / MICRO_CNY),
                "remaining_cny": str(Decimal(self.cap_micro - reserved) / MICRO_CNY)}
