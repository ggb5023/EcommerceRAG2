"""Explicit ingestion dependencies; importing this module performs no I/O."""
from __future__ import annotations

import os
from pathlib import Path

from app.configuration import require_online_budget
from app.providers import MockProvider, build_provider
from app.providers.config import load_provider_config, validate_values
from app.storage import AlibabaOSSObjectStore, FilesystemObjectStore, load_oss_config


def load_embedding_provider():
    profile = os.environ.get("RAG_PROFILE", "")
    if profile in {"v2_langchain_mock", "v2_langgraph_fixture"}:
        return MockProvider(validate_values({
            "PROVIDER_PROFILE": "mock", "CONTROL_MODEL": "mock-control",
            "EMBEDDING_MODEL": "mock-embedding", "RERANK_MODEL": "mock-rerank",
            "GENERATION_MODEL": "mock-generation",
        }))
    if profile == "v2_langchain_bailian":
        require_online_budget()
        return build_provider(load_provider_config(os.environ.get(
            "V2_PROVIDER_CONFIG", "/etc/ecommerce-rag/providers.env")))
    raise ValueError("an explicit v2 Provider profile is required")


def load_artifact_store():
    profile = os.environ.get("INGEST_STORAGE_PROFILE", "")
    if profile == "filesystem":
        root = Path(os.environ.get("INGEST_ARTIFACT_ROOT", ""))
        if not root.is_absolute():
            raise ValueError("INGEST_ARTIFACT_ROOT must be absolute")
        return FilesystemObjectStore(root)
    if profile == "oss":
        return AlibabaOSSObjectStore(load_oss_config())
    raise ValueError("INGEST_STORAGE_PROFILE must explicitly select filesystem or oss")
