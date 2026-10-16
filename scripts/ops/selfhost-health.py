"""Bounded container readiness checks with no secret-bearing diagnostics."""
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

import psycopg
from grpc_health.v1 import health_pb2, health_pb2_grpc

from app.rpc.transport import channel

try:
    kind = sys.argv[1]
    if kind == 'worker':
        with psycopg.connect(os.environ['V2_INGEST_DSN'], connect_timeout=2) as conn:
            healthy = conn.execute("SELECT EXISTS(SELECT 1 FROM ingest_worker_heartbeat WHERE worker_id=%s AND heartbeat_at>now()-interval '20 seconds' AND status IN ('idle','working'))", (os.environ['V2_INGEST_WORKER_ID'],)).fetchone()[0]
    else:
        port = '50052' if kind == 'ai' else '50053'
        with channel('localhost:' + port) as conn:
            healthy = health_pb2_grpc.HealthStub(conn).Check(health_pb2.HealthCheckRequest(), timeout=2).status == health_pb2.HealthCheckResponse.SERVING
    sys.exit(0 if healthy else 1)
except Exception:
    sys.exit(1)
