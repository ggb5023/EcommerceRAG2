"""Provider ports.

The concrete providers already satisfy these protocols; nothing is injected and no
call site changes. Deleting this module must leave the package importing fine.
"""
from __future__ import annotations

from collections.abc import Sequence
from typing import Protocol

from app.providers.contracts import (
    EmbeddingResult,
    GenerationResult,
    RerankResult,
)


class ProviderPort(Protocol):
    """检索-生成段依赖的 provider 能力（见 decisions/0029 与 0028）。"""

    def generate(self, *args: object, **kwargs: object) -> GenerationResult: ...

    def embed(self, *args: object, **kwargs: object) -> EmbeddingResult: ...

    def rerank(self, *args: object, **kwargs: object) -> Sequence[RerankResult]: ...
