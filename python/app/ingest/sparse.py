"""Serialize provider sparse weights into a pgvector `sparsevec` literal.

0029 step 2: the dimension is pinned by migration 0037 (`sparsevec(65536)`), the
non-zero budget is 2048, and an unusable payload is written as NULL — never
silently. Every rejection returns a reason so the caller can log one warning with
the chunk id; "written NULL without a word" is the failure mode this module exists
to prevent.
"""
from __future__ import annotations

import math
from collections.abc import Sequence

from app.providers.contracts import SparseWeight

# Must match sql/migrations/0037_sparse_vector_dimension.sql.
SPARSE_DIMENSION = 65536
SPARSE_MAX_NNZ = 2048
SPARSE_TOKENIZER_SUFFIX = ":sparse"


def sparse_tokenizer_id(provider: str, model: str) -> str:
    """The tokenizer identity recorded next to the vector: <provider>:<model>:sparse."""
    return f"{provider}:{model}{SPARSE_TOKENIZER_SUFFIX}"


def to_literal(weights: Sequence[SparseWeight] | None, *, dimension: int = SPARSE_DIMENSION,
               max_nnz: int = SPARSE_MAX_NNZ) -> tuple[str | None, str | None]:
    """Return (literal, None) or (None, reason).

    The literal uses pgvector's `{index:value,...}/dimension` form. pgvector is
    1-based, this module is 0-based: the boundary adds +1 when writing, so the read
    side must subtract 1. With dimension 65536 the range 0..65535 maps exactly onto
    1..65536. Duplicate token ids are rejected (the provider contract says unique).
    """
    if not weights:
        return None, "provider returned no sparse weights"
    if len(weights) > max_nnz:
        return None, f"nnz {len(weights)} exceeds the {max_nnz} limit"
    pairs: dict[int, float] = {}
    for weight in weights:
        token_id = weight.token_id
        value = weight.weight
        if not isinstance(token_id, int) or isinstance(token_id, bool):
            return None, f"token_id {token_id!r} is not an integer"
        if not 0 <= token_id < dimension:
            return None, f"token_id {token_id} is outside [0,{dimension})"
        if not isinstance(value, (int, float)) or isinstance(value, bool) or not math.isfinite(value):
            return None, f"token_id {token_id} has a non-finite weight"
        if value <= 0:
            return None, f"token_id {token_id} has a non-positive weight"
        if token_id in pairs:
            return None, f"token_id {token_id} appears more than once"
        pairs[token_id] = float(value)
    body = ",".join(f"{token_id + 1}:{value:g}" for token_id, value in sorted(pairs.items()))
    return "{" + body + "}/" + str(dimension), None
