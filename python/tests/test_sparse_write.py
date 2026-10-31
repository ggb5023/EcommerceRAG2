"""Write-path rules for chunk.sparse_embedding (0029 step 2)."""
from __future__ import annotations

import math

import pytest

from app.ingest import sparse
from app.providers.contracts import SparseWeight


def test_literal_shape_and_sorting():
    literal, reason = sparse.to_literal([SparseWeight(7, 0.5), SparseWeight(2, 1.25)])
    assert reason is None
    assert literal == "{3:1.25,8:0.5}/65536"  # pgvector indexes are 1-based


def test_tokenizer_identity_names_the_producer():
    assert sparse.sparse_tokenizer_id("bailian", "text-embedding-v3") == "bailian:text-embedding-v3:sparse"


def test_token_id_outside_the_dictionary_is_rejected():
    literal, reason = sparse.to_literal([SparseWeight(sparse.SPARSE_DIMENSION, 1.0)])
    assert literal is None and "outside" in reason
    assert sparse.to_literal([SparseWeight(-1, 1.0)])[0] is None


def test_nnz_over_the_budget_is_rejected():
    weights = [SparseWeight(index, 1.0) for index in range(sparse.SPARSE_MAX_NNZ + 1)]
    literal, reason = sparse.to_literal(weights)
    assert literal is None and "nnz" in reason


def test_empty_or_missing_payload_is_rejected_with_a_reason():
    for payload in (None, []):
        literal, reason = sparse.to_literal(payload)
        assert literal is None and reason


def test_non_finite_or_non_positive_weights_are_rejected():
    assert sparse.to_literal([SparseWeight(1, math.inf)])[0] is None
    assert sparse.to_literal([SparseWeight(1, math.nan)])[0] is None
    assert sparse.to_literal([SparseWeight(1, 0.0)])[0] is None


def test_exactly_the_limits_are_accepted():
    weights = [SparseWeight(index, 1.0) for index in range(sparse.SPARSE_MAX_NNZ)]
    weights.append(SparseWeight(sparse.SPARSE_DIMENSION - 1, 2.0))
    assert len(weights) == sparse.SPARSE_MAX_NNZ + 1
    assert sparse.to_literal(weights)[0] is None  # one over the budget
    literal, reason = sparse.to_literal(weights[:-1])
    assert reason is None and literal.endswith("/65536")


def _dsn():
    import os
    from pathlib import Path

    path = os.environ.get("V2_INTEGRATION_ENV") or os.environ.get("ECR_SPARSE_TEST_ENV")
    if not path or not Path(path).is_file():
        return None
    values = dict(
        line.strip().split("=", 1) for line in Path(path).read_text().splitlines()
        if "=" in line and not line.startswith("#")
    )
    return values.get("V2_AI_DSN") or values.get("V2_INGEST_DSN")


@pytest.mark.skipif(_dsn() is None, reason="no V2 integration env")
def test_literal_round_trips_through_postgres():
    """A literal that only looks right is not right: send it to PostgreSQL."""
    import psycopg

    literal, reason = sparse.to_literal([SparseWeight(0, 1.0), SparseWeight(65535, 2.0)])
    assert reason is None
    with psycopg.connect(_dsn()) as conn:
        # vector_dims() does not accept sparsevec; the dimension comes from the
        # literal's "/dims" suffix after PostgreSQL has accepted it.
        text = conn.execute("SELECT %s::sparsevec::text", (literal,)).fetchone()[0]
        nnz = len(text.strip("{}").split("/")[0].split(","))
        dims = int(text.rsplit("/", 1)[1])
    assert dims == 65536, dims
    assert nnz == 2, nnz
    assert text.startswith("{1:1,65536:2}"), text
