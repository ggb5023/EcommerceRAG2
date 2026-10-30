"""Write-path rules for chunk.sparse_embedding (0029 step 2)."""
from __future__ import annotations

import math

from app.ingest import sparse
from app.providers.contracts import SparseWeight


def test_literal_shape_and_sorting():
    literal, reason = sparse.to_literal([SparseWeight(7, 0.5), SparseWeight(2, 1.25)])
    assert reason is None
    assert literal == "{2:1.25,7:0.5}/65536"


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
