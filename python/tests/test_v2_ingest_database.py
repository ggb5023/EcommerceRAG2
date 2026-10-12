"""Real isolated PostgreSQL contracts; opt in with V2_INTEGRATION_ENV."""
from __future__ import annotations

import hashlib
import os
import threading
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from pathlib import Path
from uuid import uuid4

import psycopg
import pytest

from app.ingest.commands import CommandStore, command_action, payload_digest
from app.ingest.database import (
    IngestAuthorizationError,
    IngestConflictError,
    IngestDatabase,
    package_payload_hash,
)
from app.ingest.object_journal import cleanup_orphans
from app.ingest.worker import IngestWorker, parse_persisted_package
from app.providers import MockProvider
from app.providers.config import validate_values
from app.providers.contracts import ProviderError
from app.storage import FilesystemObjectStore
from rag.v1 import rag_pb2


@pytest.fixture
def db(tmp_path):
    config = os.environ.get("V2_INTEGRATION_ENV")
    if not config:
        pytest.skip("V2_INTEGRATION_ENV is required for real PostgreSQL acceptance")
    values = dict(line.split("=", 1) for line in Path(config).read_text().splitlines() if line and not line.startswith("#"))
    suffix = uuid4().hex
    with psycopg.connect(values["V2_MIGRATION_DSN"]) as conn:
        assert "v2_test" in conn.execute("SELECT current_database()").fetchone()[0]
        tenant = conn.execute("INSERT INTO tenant(name,external_id) VALUES(%s,%s) RETURNING id", ("Integration " + suffix, suffix)).fetchone()[0]
        conn.execute("INSERT INTO shop(tenant_id,id,name) VALUES(%s,'shop-test','Test shop')", (tenant,))
        user = conn.execute("INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES(%s,'test-user','admin',ARRAY['shop-test']) RETURNING id", (tenant,)).fetchone()[0]
        for role in ("merchant_admin", "knowledge_reviewer"):
            conn.execute("INSERT INTO admin_role_grant(tenant_id,user_id,role,scope_kind,granted_by) VALUES(%s,%s,%s,'tenant',%s)", (tenant, user, role, user))
    provider = MockProvider(validate_values({"PROVIDER_PROFILE": "mock", "CONTROL_MODEL": "mock-control", "EMBEDDING_MODEL": "mock-embedding", "RERANK_MODEL": "mock-rerank", "GENERATION_MODEL": "mock-generation"}))
    store = IngestDatabase(values["V2_INGEST_DSN"])
    objects = FilesystemObjectStore(tmp_path / "artifacts")
    worker = IngestWorker(store, embedding_provider=provider, artifact_store=objects)
    commands = CommandStore(store.conn_string, artifact_store=objects)

    def scope():
        with psycopg.connect(values["V2_MIGRATION_DSN"]) as conn:
            revision = conn.execute("SELECT permission_revision FROM app_user WHERE id=%s", (user,)).fetchone()[0]
        return rag_pb2.RequestContext(tenant_id=suffix, tenant_db_id=tenant, user_id="test-user", user_db_id=user, role="admin", shop_id="shop-test", allowed_shop_ids=["shop-test"], permission_revision=revision, scope_audience="ingest")

    def submit(content=b"# Delivery\n\nOrders leave within two business days.\n", *, key=None):
        sha = hashlib.sha256(content).hexdigest()
        manifest = f"schema_version: 1\nsource:\n  id: docs\n  name: Docs\npipeline_version: v2-test\ndocuments:\n  - document_id: delivery\n    title: Delivery\n    path: delivery.md\n    format: markdown\n    sha256: {sha}\n    disclosure_class: external_allowed\n"
        manifest_sha = hashlib.sha256(manifest.encode()).hexdigest()
        return store.submit(tenant_id=suffix, user_id="test-user", shop_id="shop-test", tenant_db_id=str(tenant), user_db_id=str(user), permission_revision=scope().permission_revision, idempotency_key=key or uuid4().hex, source_id="docs", source_name="Docs", pipeline_version="v2-test", manifest_yaml=manifest, manifest_sha256=manifest_sha, package_sha256=package_payload_hash(tenant, "shop-test", manifest_sha, {"delivery.md": content}), documents=[{"document_id": "delivery", "title": "Delivery", "path": "delivery.md", "source_hash": sha, "format": "markdown", "disclosure_class": "external_allowed", "effective_from": "", "effective_to": ""}], files={"delivery.md": content})

    def execute(action, resource, *, key=None, **kwargs):
        request = rag_pb2.ExecuteCommandRequest(context=scope(), command_id=key or uuid4().hex, action=action, resource_id=str(resource), reason="Reviewed in isolated V2 acceptance", **kwargs)
        request.context.command_id = request.command_id
        request.context.command_action = command_action(action)
        request.context.command_payload_sha256 = payload_digest(request)
        return commands.execute(request), request

    return type("Fixture", (), {"values": values, "tenant": tenant, "user": user, "store": store, "worker": worker, "objects": objects, "commands": commands, "scope": staticmethod(scope), "submit": staticmethod(submit), "execute": staticmethod(execute)})()


def version(db, job):
    with psycopg.connect(db.values["V2_MIGRATION_DSN"]) as conn:
        return conn.execute("SELECT v.id,v.source_hash,v.fencing_epoch,v.status,v.review_status,v.quality_json FROM document_version v JOIN ingest_job_item i ON i.tenant_id=v.tenant_id AND i.document_version_id=v.id WHERE i.tenant_id=%s AND i.job_id=%s", (db.tenant, job)).fetchone()


def ingest(db, content=None):
    job = db.submit() if content is None else db.submit(content)
    # The queue can contain jobs from other tests. Claim this fixture explicitly.
    for _ in range(100):
        claim = db.worker._claim()
        assert claim is not None
        db.worker._process(claim)
        if claim.job_id == job.job_id:
            break
    row = version(db, job.job_id)
    assert row is not None, db.store.get_status(tenant_db_id=db.tenant, job_id=job.job_id, shop_id="shop-test")
    return job, row


def approve_publish(db, row):
    args = {"expected_hash": row[1], "expected_fencing_epoch": row[2]}
    result, _ = db.execute(rag_pb2.INGEST_COMMAND_ACTION_REVIEW, row[0], review_decision="approved", disclosure_class="external_allowed", external_allowed=True, **args)
    assert not result.error_code
    result, request = db.execute(rag_pb2.INGEST_COMMAND_ACTION_PUBLISH, row[0], **args)
    assert result.status == "active", result
    return result, request


def test_service_database_write_ownership(db):
    db.store.verify_schema()
    denied = {
        "V2_GATEWAY_DSN": ("source", "document", "document_version", "chunk", "ingest_job", "ingest_command_result"),
        "V2_AI_DSN": ("document", "ingest_job", "control_command", "admin_audit"),
        "V2_INGEST_DSN": ("conversation", "app_user", "admin_audit", "control_command"),
    }
    for key, tables in denied.items():
        with psycopg.connect(db.values[key], autocommit=True) as conn:
            assert conn.execute("SELECT rolsuper FROM pg_roles WHERE rolname=current_user").fetchone() == (False,)
            for table in tables:
                column = "tenant_id"
                with pytest.raises(psycopg.errors.InsufficientPrivilege):
                    conn.execute(f"UPDATE {table} SET {column}={column} WHERE false")


def test_ingest_review_publish_replay_rollback_revoke(db):
    _first, row = ingest(db)
    assert row[3:5] == ("ready", "pending")
    assert row[5]["embedding_status"] == "passed"
    with psycopg.connect(db.values["V2_AI_DSN"]) as conn:
        assert conn.execute("SELECT count(*) FROM chunk c JOIN document d ON d.tenant_id=c.tenant_id AND d.id=c.doc_id AND d.active_version_id=c.version_id WHERE c.tenant_id=%s", (db.tenant,)).fetchone() == (0,)
    result, request = approve_publish(db, row)
    request.context.CopyFrom(db.scope())
    request.context.command_id, request.context.command_action, request.context.command_payload_sha256 = request.command_id, command_action(request.action), payload_digest(request)
    replay = db.commands.execute(request)
    assert replay.idempotent and replay.fencing_epoch == result.fencing_epoch
    assert db.commands.get(request.context, request.command_id).status == "active"
    wrong_scope = db.scope()
    wrong_scope.command_id = request.command_id
    wrong_scope.command_action = request.context.command_action
    wrong_scope.command_payload_sha256 = "0" * 64
    with pytest.raises(IngestAuthorizationError):
        db.commands.get(wrong_scope, request.command_id)
    request.reason = "Different payload with the same command identifier"
    request.context.command_payload_sha256 = payload_digest(request)
    with pytest.raises(IngestConflictError):
        db.commands.execute(request)
    _, second = ingest(db, b"# Delivery\n\nOrders leave within three business days.\n")
    approve_publish(db, second)
    result, _ = db.execute(rag_pb2.INGEST_COMMAND_ACTION_ROLLBACK, row[0], expected_hash=row[1], expected_fencing_epoch=row[2] + 1)
    assert result.status == "active", result
    result, _ = db.execute(rag_pb2.INGEST_COMMAND_ACTION_REVOKE, row[0], expected_hash=row[1], expected_fencing_epoch=result.fencing_epoch)
    assert result.status == "revoked"
    with psycopg.connect(db.values["V2_AI_DSN"]) as conn:
        assert conn.execute("SELECT count(*) FROM document WHERE tenant_id=%s AND active_version_id IS NOT NULL", (db.tenant,)).fetchone() == (0,)
    assert cleanup_orphans(db.objects, db.store.conn_string) == 0


def test_duplicate_submission_conflict_and_stale_scope(db):
    first = db.submit(key="duplicate")
    assert db.submit(key="duplicate").job_id == first.job_id
    with pytest.raises(IngestConflictError):
        db.submit(b"# Different\n\nDifferent bytes.\n", key="duplicate")
    stale = db.scope()
    with psycopg.connect(db.values["V2_MIGRATION_DSN"]) as conn:
        conn.execute("UPDATE app_user SET revoked_at=now() WHERE id=%s", (db.user,))
    request = rag_pb2.ExecuteCommandRequest(context=stale, command_id="stale", action=rag_pb2.INGEST_COMMAND_ACTION_CANCEL, resource_id=str(first.job_id), reason="Cancel after permission was revoked")
    request.context.command_id = request.command_id
    request.context.command_action = command_action(request.action)
    request.context.command_payload_sha256 = payload_digest(request)
    with pytest.raises(IngestAuthorizationError):
        db.commands.execute(request)


def test_expired_worker_is_fenced_and_new_owner_recovers(db):
    job = db.submit()
    for _ in range(100):
        claim = db.worker._claim()
        assert claim is not None
        if claim.job_id == job.job_id:
            break
        db.worker._process(claim)
    with psycopg.connect(db.values["V2_MIGRATION_DSN"]) as conn:
        conn.execute("UPDATE ingest_job SET lease_expires_at=now()-interval '1 second' WHERE id=%s", (job.job_id,))
        conn.execute("UPDATE ingest_job_item SET lease_expires_at=now()-interval '1 second' WHERE job_id=%s", (job.job_id,))
    parsed, sha = parse_persisted_package(claim.manifest, claim.files, tenant=db.scope().tenant_id, shop="shop-test")
    with pytest.raises(RuntimeError, match="stale"):
        db.worker._publish_reviewable(claim, parsed, sha)
    takeover = db.worker._claim()
    assert takeover.fencing_epoch == claim.fencing_epoch + 1
    db.worker._process(takeover)
    assert version(db, job.job_id)[3] == "ready"


def test_cancel_publish_race_has_single_result(db):
    job, row = ingest(db)
    result, _ = db.execute(rag_pb2.INGEST_COMMAND_ACTION_REVIEW, row[0], expected_hash=row[1], expected_fencing_epoch=row[2], review_decision="approved", disclosure_class="external_allowed", external_allowed=True)
    assert not result.error_code
    barrier = threading.Barrier(2)
    def command(action, resource, **kwargs):
        barrier.wait()
        key = uuid4().hex
        try:
            return db.execute(action, resource, key=key, **kwargs)[0]
        except IngestAuthorizationError:
            # Publication can bump the revision while the other command
            # waits on the knowledge lock. Reauthorize the same command.
            return db.execute(action, resource, key=key, **kwargs)[0]
    with ThreadPoolExecutor(2) as pool:
        publish = pool.submit(command, rag_pb2.INGEST_COMMAND_ACTION_PUBLISH, row[0], expected_hash=row[1], expected_fencing_epoch=row[2])
        cancel = pool.submit(command, rag_pb2.INGEST_COMMAND_ACTION_CANCEL, job.job_id, expected_fencing_epoch=1)
        published, cancelled = publish.result(), cancel.result()
    assert (published.status, cancelled.status) in {("active", "done"), ("rejected", "cancelled")}
    with psycopg.connect(db.values["V2_AI_DSN"]) as conn:
        active = conn.execute("SELECT active_version_id FROM document WHERE tenant_id=%s", (db.tenant,)).fetchone()[0]
    assert (active == row[0]) == (published.status == "active")


def test_publication_invalidates_stale_cancel_scope(db):
    job, row = ingest(db)
    stale = db.scope()
    approve_publish(db, row)
    request = rag_pb2.ExecuteCommandRequest(context=stale, command_id="cancel-after-publish", action=rag_pb2.INGEST_COMMAND_ACTION_CANCEL, resource_id=str(job.job_id), expected_fencing_epoch=1, reason="Cancel after publication in isolated acceptance")
    request.context.command_id = request.command_id
    request.context.command_action = command_action(request.action)
    request.context.command_payload_sha256 = payload_digest(request)
    with pytest.raises(IngestAuthorizationError):
        db.commands.execute(request)
    result, _ = db.execute(request.action, job.job_id, key=request.command_id, expected_fencing_epoch=1)
    assert result.status == "done"
    assert version(db, job.job_id)[3] == "active"


def test_artifact_failure_rolls_back_and_retry_recovers(db, monkeypatch):
    job = db.submit()
    for _ in range(100):
        claim = db.worker._claim()
        assert claim is not None
        if claim.job_id == job.job_id:
            break
        db.worker._process(claim)
    original = db.objects.put_bytes
    calls = 0
    def broken(*args, **kwargs):
        nonlocal calls
        calls += 1
        if calls == 2:
            raise RuntimeError("injected object write failure")
        return original(*args, **kwargs)
    monkeypatch.setattr(db.objects, "put_bytes", broken)
    db.worker._process(claim)
    assert version(db, job.job_id) is None
    assert not list(db.objects.root.rglob("artifact-manifest/*"))
    monkeypatch.setattr(db.objects, "put_bytes", original)
    with psycopg.connect(db.values["V2_MIGRATION_DSN"]) as conn:
        conn.execute("UPDATE ingest_job SET next_attempt_at=now() WHERE id=%s", (job.job_id,))
    takeover = db.worker._claim()
    assert takeover.job_id == job.job_id
    db.worker._process(takeover)
    assert version(db, job.job_id)[3] == "ready"


@pytest.mark.parametrize("failure", [
    "authentication", "budget_blocked", "budget_exhausted", "timeout", "rate_limited",
    "upstream_error", "invalid_response", "unknown_sensitive_code",
    "missing_vector", "wrong_dimension", "zero_vector", "nonfinite_vector",
])
def test_embedding_failure_stops_paid_retries_and_explicit_retry_recovers(db, failure):
    job = db.submit()
    for _ in range(100):
        claim = db.worker._claim()
        assert claim is not None
        if claim.job_id == job.job_id:
            break
        db.worker._process(claim)
    else:
        pytest.fail("submitted job was not claimed")
    provider = db.worker.embedding_provider
    calls = 0

    class BrokenEmbedding:
        async def embed(self, texts, **kwargs):
            nonlocal calls
            calls += 1
            if failure in {"missing_vector", "wrong_dimension", "zero_vector", "nonfinite_vector"}:
                results = list(await provider.embed(texts, **kwargs))
                if failure == "missing_vector":
                    return []
                if failure == "wrong_dimension":
                    results[0] = replace(results[0], dimensions=768)
                else:
                    fill = 0.0 if failure == "zero_vector" else float("nan")
                    results[0] = replace(results[0], dense=(fill,) * 1024)
                return results
            raise ProviderError(failure, "sensitive vendor response must not persist",
                                slot="embedding", retryable=True)

    db.worker.embedding_provider = BrokenEmbedding()
    db.worker._process(claim)
    db.worker.embedding_provider = provider
    expected = ("failed" if failure == "unknown_sensitive_code" else
                "invalid_response" if failure in {"missing_vector", "wrong_dimension", "zero_vector", "nonfinite_vector"}
                else failure)
    with psycopg.connect(db.values["V2_MIGRATION_DSN"]) as conn:
        assert conn.execute("SELECT status,stage,retry_count,error_code,error,owner_token,lease_expires_at "
                            "FROM ingest_job WHERE tenant_id=%s AND id=%s", (db.tenant, job.job_id)).fetchone() == (
            "failed", "failed", 1, f"provider_{expected}", "embedding failed", None, None)
        assert conn.execute("SELECT status,retry_count,error_code,document_version_id,owner_token "
                            "FROM ingest_job_item WHERE tenant_id=%s AND job_id=%s", (db.tenant, job.job_id)).fetchone() == (
            "failed", 1, f"provider_{expected}", None, None)
        assert conn.execute("SELECT count(*) FROM chunk WHERE tenant_id=%s", (db.tenant,)).fetchone() == (0,)
    assert version(db, job.job_id) is None
    assert not list(db.objects.root.rglob("*.json"))
    # Queue polling cannot select the failed job, even for retryable timeouts.
    for _ in range(100):
        candidate = db.worker._claim()
        if candidate is None:
            break
        assert candidate.job_id != job.job_id
        db.worker._process(candidate)
    else:
        pytest.fail("queue did not drain")
    assert calls == 1
    result, _ = db.execute(rag_pb2.INGEST_COMMAND_ACTION_RETRY, job.job_id,
                           expected_fencing_epoch=claim.fencing_epoch)
    assert result.status == "pending"
    resumed = db.worker._claim()
    assert resumed.job_id == job.job_id
    assert resumed.fencing_epoch == claim.fencing_epoch + 1
    db.worker._process(resumed)
    assert version(db, job.job_id)[3] == "ready"


def test_unknown_commit_outcome_preserves_committed_artifacts(db, monkeypatch):
    job = db.submit()
    original = db.worker._publish_reviewable
    def response_lost(*args):
        original(*args)
        raise RuntimeError("commit succeeded but its response was lost")
    monkeypatch.setattr(db.worker, "_publish_reviewable", response_lost)
    for _ in range(100):
        claim = db.worker._claim()
        assert claim is not None
        db.worker._process(claim)
        if claim.job_id == job.job_id:
            break
    row = version(db, job.job_id)
    assert row[3] == "ready"
    cleanup_orphans(db.objects, db.store.conn_string)
    for artifact in row[5]["artifact_bundle"]["artifacts"]:
        db.objects.get_bytes(artifact["object_key"], expected_sha256=artifact["sha256"])
    approve_publish(db, row)


def test_corrupt_artifact_publication_is_rejected_durably(db):
    _, row = ingest(db)
    args = {"expected_hash": row[1], "expected_fencing_epoch": row[2]}
    reviewed, _ = db.execute(rag_pb2.INGEST_COMMAND_ACTION_REVIEW, row[0], review_decision="approved", disclosure_class="external_allowed", external_allowed=True, **args)
    assert reviewed.review_status == "approved"
    raw = next(a for a in row[5]["artifact_bundle"]["artifacts"] if a["artifact_type"] == "raw")
    (db.objects.root / raw["object_key"]).write_bytes(b"tampered")
    result, request = db.execute(rag_pb2.INGEST_COMMAND_ACTION_PUBLISH, row[0], **args)
    assert result.status == "rejected"
    assert db.commands.get(request.context, request.command_id).error_code == "precondition_failed"


def test_concurrent_claim_does_not_duplicate_work(db):
    job = db.submit()
    with ThreadPoolExecutor(2) as pool:
        claims = list(pool.map(lambda _: db.worker._claim(), range(2)))
    selected = [claim for claim in claims if claim and claim.job_id == job.job_id]
    assert len(selected) == 1
    db.worker._process(selected[0])
    assert version(db, job.job_id)[3] == "ready"
