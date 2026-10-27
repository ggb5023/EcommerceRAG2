"""Explicit provider-backed v2 graph construction.

Loading this module does not read credentials.  Callers opt in by invoking
``load_provider_service`` with the reviewed provider configuration path.
"""
from __future__ import annotations

import os
from pathlib import Path

from app.chains.provider import build_provider_chains
from app.providers import MockProvider, build_provider
from app.providers.config import (
    CONFIG_PATH,
    ensure_secure_config_file,
    load_provider_config,
    validate_values,
)
from app.retrieval import PostgresRetriever

from .checkpoint import (
    CheckpointStore,
    InMemoryCheckpointStore,
    PostgresCheckpointStore,
)
from .service import LangGraphRagService


def load_checkpoint_store(connections=None) -> CheckpointStore:
    """Select durable storage only when an operator explicitly supplies a DSN.

    ``connections`` is the role-bound ai pool of the merged ai process; it only
    supplies the connection factory, never a different database identity.
    """
    dsn = os.environ.get("V2_CHECKPOINT_DSN", "").strip()
    if not dsn:
        return InMemoryCheckpointStore()
    if connections is None:
        return PostgresCheckpointStore(dsn)
    return PostgresCheckpointStore(dsn, connect=connections.connect)


def load_provider_service(
    path: str | Path = CONFIG_PATH,
    *,
    checkpoint_store: CheckpointStore | None = None,
    connections=None,
) -> LangGraphRagService:
    """Build a provider-backed service without logging configuration values."""
    config = load_provider_config(path)
    if config.profile == "aliyun-bailian":
        ensure_secure_config_file(path)
    provider = build_provider(config)
    retriever = _load_retriever(connections)
    chains = build_provider_chains(
        control=provider,
        embedding=provider,
        rerank=provider,
        generation=provider,
        retriever=retriever,
    )
    return LangGraphRagService(provider_chains=chains, checkpoint_store=checkpoint_store)


def build_mock_provider_service(
    *, checkpoint_store: CheckpointStore | None = None, connections=None,
) -> LangGraphRagService:
    """Build the explicit offline Provider profile without reading a file."""
    config = validate_values({
        "PROVIDER_PROFILE": "mock",
        "CONTROL_MODEL": "mock-control",
        "EMBEDDING_MODEL": "mock-embedding",
        "RERANK_MODEL": "mock-rerank",
        "GENERATION_MODEL": "mock-generation",
    })
    provider = MockProvider(config)
    retriever = _load_retriever(connections)
    chains = build_provider_chains(
        control=provider,
        embedding=provider,
        rerank=provider,
        generation=provider,
        retriever=retriever,
    )
    return LangGraphRagService(provider_chains=chains, checkpoint_store=checkpoint_store)


def _load_retriever(connections=None) -> PostgresRetriever | None:
    """Load persistent retrieval only when an operator supplies a DSN.

    The fixture profile remains completely offline when the variable is absent.
    A configured DSN is checked before the service starts so a missing
    migration cannot silently fall back to the in-memory candidate set.
    """
    dsn = os.environ.get("V2_RETRIEVER_DSN", "").strip()
    if not dsn:
        return None
    retriever = PostgresRetriever(dsn, connect=connections.connect) if connections is not None else PostgresRetriever(dsn)
    retriever.verify_schema()
    return retriever
