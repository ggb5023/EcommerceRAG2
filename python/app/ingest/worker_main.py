"""Independent, signal-aware ingestion worker entrypoint."""
from __future__ import annotations

import argparse
import signal

from app.configuration import verify_database
from app.ingest.object_journal import cleanup_orphans
from app.ingest.worker import IngestWorker


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    worker = IngestWorker.from_env()
    if worker is None:
        raise ValueError("V2_INGEST_DSN is required")
    verify_database(worker.database.conn_string, "rag_v2_ingest")
    worker.database.verify_schema()
    signal.signal(signal.SIGTERM, lambda *_: worker.stop())
    signal.signal(signal.SIGINT, lambda *_: worker.stop())
    if args.once:
        cleanup_orphans(worker.artifact_store, worker.database.conn_string,
                        connect=worker.database.connector)
        claim = worker._claim()
        if claim:
            worker._process(claim)
    else:
        worker.run()


if __name__ == "__main__":
    main()
