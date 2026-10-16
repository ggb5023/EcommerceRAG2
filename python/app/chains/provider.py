"""LangChain Runnable adapters for the repository Provider contracts.

The vendor-neutral Provider classes remain the parameter and error authority.
These adapters only translate their typed results to LangChain values, so a
future model swap cannot silently drop request IDs, usage, scores, or the
1024-dimensional dense&sparse embedding contract.
"""
from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

from langchain_core.runnables import Runnable, RunnableLambda

from app.providers.contracts import (
    ControlProvider,
    EmbeddingProvider,
    GenerationProvider,
    RerankProvider,
)


def _usage(value: Any) -> dict[str, int] | None:
    if value is None:
        return None
    return {
        key: int(number)
        for key, number in (
            ("input_tokens", value.input_tokens),
            ("output_tokens", value.output_tokens),
            ("total_tokens", value.total_tokens),
        )
        if number is not None
    }


def _metadata(*, model: str, request_id: str, usage: Any) -> dict[str, Any]:
    """Return only non-secret provider metadata for graph traces."""
    return {
        "model": model,
        "request_id": request_id,
        "usage": _usage(usage),
    }


def _mapping(payload: object, field: str) -> Mapping[str, Any]:
    if not isinstance(payload, Mapping):
        raise TypeError(f"{field} input must be an object")
    return payload


@dataclass(frozen=True)
class ProviderChainSet:
    """LangChain boundaries for all four AI slots."""

    control: Runnable
    embedding: Runnable
    rerank: Runnable
    generation: Runnable
    retriever: Any | None = None


def build_provider_chains(
    *,
    control: ControlProvider,
    embedding: EmbeddingProvider,
    rerank: RerankProvider,
    generation: GenerationProvider,
    retriever: Any | None = None,
) -> ProviderChainSet:
    """Build async-only Runnables around typed Provider implementations.

    Provider calls are intentionally exposed through ``ainvoke``.  Calling a
    network provider from a synchronous LangGraph or gRPC handler would hide
    cancellation and event-loop ownership; the v2 service therefore uses its
    explicit async entrypoint for this chain set.
    """

    async def run_control(payload: object) -> dict[str, Any]:
        value = _mapping(payload, "control")
        result = await control.control(
            value.get("messages", []),
            response_schema=value.get("response_schema"),
            timeout_s=value.get("timeout_s"),
            cancel_event=value.get("cancel_event"),
        )
        return {
            "structured": dict(result.structured),
            "provider": _metadata(
                model=result.model, request_id=result.request_id, usage=result.usage
            ),
            "finish_reason": result.finish_reason,
        }

    async def run_embedding(payload: object) -> dict[str, Any]:
        value = _mapping(payload, "embedding")
        texts = value.get("texts", [])
        results = await embedding.embed(
            texts,
            text_type=value.get("text_type", "document"),
            dimensions=value.get("dimensions", 1024),
            output_type=value.get("output_type", "dense&sparse"),
            instruct_version=value.get("instruct_version", ""),
            cancel_event=value.get("cancel_event"),
        )
        return {
            "items": [
                {
                    "dense": list(item.dense),
                    "sparse": [
                        {"token_id": weight.token_id, "weight": weight.weight}
                        for weight in item.sparse
                    ],
                    "dimensions": item.dimensions,
                    "provider": _metadata(
                        model=item.model,
                        request_id=item.request_id,
                        usage=item.usage,
                    ),
                }
                for item in results
            ]
        }

    async def run_rerank(payload: object) -> dict[str, Any]:
        value = _mapping(payload, "rerank")
        result = await rerank.rerank(
            value.get("query", ""),
            value.get("candidates", []),
            top_n=value.get("top_n"),
            instruct=value.get("instruct", ""),
            timeout_s=value.get("timeout_s"),
            cancel_event=value.get("cancel_event"),
        )
        return {
            "items": [{"index": item.index, "score": item.score} for item in result.items],
            "provider": _metadata(
                model=result.model, request_id=result.request_id, usage=result.usage
            ),
        }

    async def run_generation(payload: object) -> dict[str, Any]:
        value = _mapping(payload, "generation")
        result = await generation.generate(
            value.get("messages", []),
            response_schema=value.get("response_schema"),
            timeout_s=value.get("timeout_s"),
            cancel_event=value.get("cancel_event"),
        )
        return {
            "text": result.text,
            "structured": dict(result.structured) if result.structured is not None else None,
            "provider": _metadata(
                model=result.model, request_id=result.request_id, usage=result.usage
            ),
            "finish_reason": result.finish_reason,
        }

    def sync_only(_: object) -> None:
        raise RuntimeError("provider LangChain chains require ainvoke")

    return ProviderChainSet(
        control=RunnableLambda(sync_only, afunc=run_control, name="provider-control"),
        embedding=RunnableLambda(sync_only, afunc=run_embedding, name="provider-embedding"),
        rerank=RunnableLambda(sync_only, afunc=run_rerank, name="provider-rerank"),
        generation=RunnableLambda(sync_only, afunc=run_generation, name="provider-generation"),
        retriever=retriever,
    )
