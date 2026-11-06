"""mTLS-only gRPC read API for shared market observations."""
from __future__ import annotations

from datetime import UTC, datetime

import grpc

from app.intel.database import IntelDatabase
from app.rpc.server import new_server
from rag.v1 import intel_pb2, intel_pb2_grpc


class IntelService(intel_pb2_grpc.IntelServiceServicer):
    def __init__(self, database: IntelDatabase) -> None:
        self.database = database

    def ListObservations(self, request, context):
        limit = request.limit or 50
        if not 1 <= limit <= 100:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "limit must be between 1 and 100")
        since = None
        if request.since.strip():
            try:
                since = datetime.fromisoformat(request.since)
            except ValueError:
                context.abort(grpc.StatusCode.INVALID_ARGUMENT, "since must be RFC3339")
            if since.tzinfo is None:
                since = since.replace(tzinfo=UTC)
        try:
            rows = self.database.list_observations(
                request.market.strip(), request.category.strip(), request.platform.strip(),
                since, limit,
            )
        except Exception:  # noqa: BLE001 - database details never cross the RPC boundary
            context.abort(grpc.StatusCode.UNAVAILABLE, "observations unavailable")
        return intel_pb2.ListObservationsResponse(
            items=[
                intel_pb2.Observation(
                    market=row["market"], category=row["category"], platform=row["platform"],
                    source_url=row["source_url"], title=row["title"], score=row["score"],
                    published_date=row["published_date"], observed_at=row["observed_at"],
                )
                for row in rows
            ]
        )


def build_intel_server(database: IntelDatabase):
    server = new_server("rag.v1.IntelService", "V2_INTEL_RPC_WORKERS")
    intel_pb2_grpc.add_IntelServiceServicer_to_server(IntelService(database), server)
    return server
