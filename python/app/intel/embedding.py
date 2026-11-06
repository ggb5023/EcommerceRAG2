"""Dense-embedding helpers shared by the intel worker and read API."""
from __future__ import annotations

import math
from collections.abc import Sequence

from app.providers.contracts import EmbeddingProvider, EmbeddingResult, ProviderError
from app.providers.usage import UsageRecorder

EMBEDDING_DIMENSIONS = 1024


def observation_text(title: str, content: str) -> str:
    return "\n".join(part for part in (title, content) if part).strip()


def vector_literal(result: EmbeddingResult) -> str | None:
    """Return a pgvector literal, or None for an unusable dense vector."""
    if result.dimensions != EMBEDDING_DIMENSIONS:
        return None
    values = tuple(float(value) for value in result.dense)
    if (len(values) != EMBEDDING_DIMENSIONS
            or not all(math.isfinite(value) for value in values)
            or not any(values)):
        return None
    return "[" + ",".join(str(value) for value in values) + "]"


def token_totals(results: Sequence[EmbeddingResult]) -> tuple[int | None, int | None]:
    """Sum provider-reported tokens; None stays None when no row reports it."""
    inputs = [row.usage.input_tokens if row.usage else row.usage_tokens for row in results]
    outputs = [row.usage.output_tokens if row.usage else None for row in results]
    return _sum_known(inputs), _sum_known(outputs)


def _sum_known(values: Sequence[int | None]) -> int | None:
    known = [value for value in values if value is not None]
    return sum(known) if known else None


def record_embedding_usage(usage: UsageRecorder | None, results: Sequence[EmbeddingResult],
                           model: str, http_status: int | None, error_code: str | None) -> None:
    if usage is None:
        return
    inputs, outputs = token_totals(results)
    used_model = results[0].model if results else model
    usage.record(tenant_id=None, slot="embedding", model=used_model or model or "unknown",
                 input_tokens=inputs, output_tokens=outputs,
                 http_status=http_status, error_code=error_code)


async def embed_dense(provider: EmbeddingProvider, texts: Sequence[str],
                      *, text_type: str) -> Sequence[EmbeddingResult]:
    results = await provider.embed(
        texts, text_type=text_type, dimensions=EMBEDDING_DIMENSIONS, output_type="dense",
    )
    if len(results) != len(texts):
        raise ProviderError("invalid_response", "embedding shape mismatch", slot="embedding")
    return results
