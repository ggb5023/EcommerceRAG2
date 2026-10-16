import json
import os
import socket
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import grpc

from app.rag_service import rag_pb2
from rag.v1 import rag_pb2_grpc


class AuthorityHandler(BaseHTTPRequestHandler):
    requests = 0

    def do_POST(self):
        type(self).requests += 1
        length = int(self.headers.get("Content-Length", "0"))
        self.rfile.read(length)
        body = json.dumps({"allowed": True}).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        return


def _free_grpc_address():
    listener = socket.socket()
    listener.bind(("127.0.0.1", 0))
    address = f"127.0.0.1:{listener.getsockname()[1]}"
    listener.close()
    return address


def test_v2_grpc_is_a_real_cross_process_boundary():
    root = Path(__file__).resolve().parents[2]
    authority = ThreadingHTTPServer(("127.0.0.1", 0), AuthorityHandler)
    authority_thread = threading.Thread(target=authority.serve_forever, daemon=True)
    authority_thread.start()
    address = _free_grpc_address()
    process = subprocess.Popen(
        [sys.executable, "-m", "app.v2_grpc_server"],
        cwd=root,
        env={
            "PATH": os.environ["PATH"],
            "PYTHONPATH": str(root / "python"),
            "APP_ENV": "test",
            "RAG_PROFILE": "v2_langgraph_fixture",
            "V2_GRPC_ADDR": address,
            "AUTHORITY_HTTP_BASE": f"http://127.0.0.1:{authority.server_port}",
        },
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    context = rag_pb2.RequestContext(
        request_id="v2-process-test",
        tenant_id="v2-demo-tenant",
        user_id="dev-user",
        allowed_shop_ids=["shop-demo"],
        shop_id="shop-demo",
        role="operator",
        permission_revision="1",
        enforce_document_scope=True,
        allowed_document_ids=["v2-doc-bottle"],
        conversation_id="conversation-checkpoint",
        turn_id="turn-checkpoint",
        execution_id="execution-checkpoint",
    )
    try:
        channel = grpc.insecure_channel(address)
        client = rag_pb2_grpc.RagServiceStub(channel)
        deadline = time.monotonic() + 10
        while True:
            try:
                saved = client.SaveCheckpoint(
                    rag_pb2.SaveCheckpointRequest(context=context, pending_kind="ASKING"),
                    timeout=0.5,
                )
                break
            except grpc.RpcError as error:
                if time.monotonic() >= deadline or error.code() not in {
                    grpc.StatusCode.UNAVAILABLE,
                    grpc.StatusCode.DEADLINE_EXCEEDED,
                }:
                    raise
                time.sleep(0.05)
        assert saved.accepted is True
        assert saved.thread_id == "v2-demo-tenant:conversation-checkpoint:turn-checkpoint"
        resumed = client.ResumeCheckpoint(
            rag_pb2.ResumeCheckpointRequest(context=context, pending_kind="ASKING"),
            timeout=2,
        )
        assert resumed.accepted is True
        assert resumed.thread_id == saved.thread_id
        deadline = time.monotonic() + 10
        while True:
            try:
                understood = client.Understand(
                    rag_pb2.UnderstandRequest(context=context, query="保温杯容量是多少？"),
                    timeout=0.5,
                )
                break
            except grpc.RpcError as error:
                if time.monotonic() >= deadline or error.code() not in {
                    grpc.StatusCode.UNAVAILABLE,
                    grpc.StatusCode.DEADLINE_EXCEEDED,
                }:
                    raise
                time.sleep(0.05)
        assert understood.intent == "knowledge"
        stream = client.Search(
            rag_pb2.SearchRequest(
                context=context,
                query=understood.rewritten_query,
                response_purpose="customer_reply",
            ),
            timeout=2,
        )
        responses = list(stream)
        evidence = [item for response in responses for item in response.evidence]
        assert responses[-1].complete is True
        assert [item.document_id for item in evidence] == ["v2-doc-bottle"]
        generated = list(
            client.Generate(
                rag_pb2.GenerateRequest(
                    context=context,
                    query=understood.rewritten_query,
                    evidence=evidence,
                    response_purpose="customer_reply",
                ),
                timeout=2,
            )
        )
        assert any(event.HasField("done") and event.done for event in generated)
        assert any(event.HasField("can_copy") and not event.can_copy for event in generated)
        channel.close()
        assert AuthorityHandler.requests >= 3
    finally:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
        authority.shutdown()
        authority.server_close()
        authority_thread.join(timeout=5)
