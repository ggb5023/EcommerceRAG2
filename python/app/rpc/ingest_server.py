"""Separate ingestion RPC process; no worker or AI model graph is started."""
from __future__ import annotations

import os

from app.configuration import verify_database
from app.ingest.database import IngestDatabase
from app.ingest.runtime import load_artifact_store
from app.rpc.ingest import IngestService
from app.rpc.server import new_server, serve
from rag.v1 import rag_pb2_grpc


def build_server():
    database = IngestDatabase.from_env()
    if database is None:
        raise ValueError("V2_INGEST_DSN is required")
    verify_database(database.conn_string, "rag_v2_ingest")
    database.verify_schema()
    server = new_server("rag.v1.IngestService", "V2_INGEST_RPC_WORKERS")
    service = IngestService(database, artifact_store=load_artifact_store())
    rag_pb2_grpc.add_IngestServiceServicer_to_server(service, server)
    return server


def main():
    serve(build_server(), os.environ.get("V2_INGEST_GRPC_ADDR", "127.0.0.1:50053"))


if __name__ == "__main__":
    main()
