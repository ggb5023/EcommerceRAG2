"""Separate ingestion RPC process; no worker or AI model graph is started."""
from __future__ import annotations

import os

from app.configuration import verify_database
from app.ingest.database import IngestDatabase
from app.ingest.runtime import load_artifact_store
from app.rpc.ingest import IngestService
from app.rpc.server import new_server, serve
from rag.v1 import rag_pb2_grpc


def load_ingest_database() -> IngestDatabase:
    """Load the ingest adapter; the database role is verified by entrypoints."""
    database = IngestDatabase.from_env()
    if database is None:
        raise ValueError("V2_INGEST_DSN is required")
    database.verify_schema()
    return database


def build_ingest_service(database: IngestDatabase) -> IngestService:
    return IngestService(database, artifact_store=load_artifact_store())


def ingest_server(service: IngestService):
    server = new_server("rag.v1.IngestService", "V2_INGEST_RPC_WORKERS")
    rag_pb2_grpc.add_IngestServiceServicer_to_server(service, server)
    return server


def build_server():
    database = load_ingest_database()
    verify_database(database.conn_string, "rag_v2_ingest")
    return ingest_server(build_ingest_service(database))


def main():
    serve(build_server(), os.environ.get("V2_INGEST_GRPC_ADDR", "127.0.0.1:50053"))


if __name__ == "__main__":
    main()
