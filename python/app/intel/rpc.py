"""mTLS-only gRPC read API for shared market observations."""
from __future__ import annotations

import asyncio
from datetime import UTC, datetime

import grpc

from app.intel.database import IntelDatabase
from app.intel.embedding import embed_dense, record_embedding_usage, vector_literal
from app.providers.contracts import EmbeddingProvider, ProviderError
from app.providers.usage import UsageRecorder
from app.rpc.server import new_server
from rag.v1 import intel_pb2, intel_pb2_grpc

MAX_QUERY_CHARS = 500


class IntelService(intel_pb2_grpc.IntelServiceServicer):
    def __init__(self, database: IntelDatabase, embedding: EmbeddingProvider | None = None,
                 usage: UsageRecorder | None = None, embedding_model: str = "unknown") -> None:
        self.database = database
        self.embedding = embedding
        self.usage = usage
        self.embedding_model = embedding_model or "unknown"

    def _query_vector(self, query: str) -> tuple[str | None, bool]:
        """Embed one query; any failure degrades to time order, never an RPC error."""
        if self.embedding is None:
            return None, True
        try:
            results = asyncio.run(embed_dense(self.embedding, [query], text_type="query"))
        except ProviderError as error:
            record_embedding_usage(self.usage, [], self.embedding_model, error.status, error.code)
            return None, True
        except Exception:  # noqa: BLE001 - a query embedding must not break the read
            record_embedding_usage(self.usage, [], self.embedding_model, None, "worker_error")
            return None, True
        literal = vector_literal(results[0]) if results else None
        if literal is None:
            record_embedding_usage(self.usage, list(results), self.embedding_model, None, "invalid_response")
            return None, True
        record_embedding_usage(self.usage, results, self.embedding_model, None, None)
        return literal, False

    def ListObservations(self, request, context):
        limit = request.limit or 50
        if not 1 <= limit <= 100:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "limit must be between 1 and 100")
        query = request.query.strip()
        if len(query) > MAX_QUERY_CHARS:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "query is too long")
        since = None
        if request.since.strip():
            try:
                since = datetime.fromisoformat(request.since)
            except ValueError:
                context.abort(grpc.StatusCode.INVALID_ARGUMENT, "since must be RFC3339")
            if since.tzinfo is None:
                since = since.replace(tzinfo=UTC)
        query_vector, degraded = (None, False)
        if query:
            query_vector, degraded = self._query_vector(query)
        try:
            rows = self.database.list_observations(
                request.market.strip(), request.category.strip(), request.platform.strip(),
                since, limit, query_vector,
            )
        except Exception:  # noqa: BLE001 - database details never cross the RPC boundary
            context.abort(grpc.StatusCode.UNAVAILABLE, "observations unavailable")
        return intel_pb2.ListObservationsResponse(
            query_degraded=degraded,
            items=[
                intel_pb2.Observation(
                    market=row["market"], category=row["category"], platform=row["platform"],
                    source_url=row["source_url"], title=row["title"], score=row["score"],
                    published_date=row["published_date"], observed_at=row["observed_at"],
                    similarity=row.get("similarity", 0.0),
                )
                for row in rows
            ],
        )


def build_intel_server(database: IntelDatabase, *, embedding: EmbeddingProvider | None = None,
                       usage: UsageRecorder | None = None, embedding_model: str = "unknown"):
    server = new_server("rag.v1.IntelService", "V2_INTEL_RPC_WORKERS")
    intel_pb2_grpc.add_IntelServiceServicer_to_server(
        IntelService(database, embedding, usage, embedding_model), server)
    return server
