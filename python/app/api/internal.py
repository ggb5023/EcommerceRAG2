"""FastAPI endpoints for health and local v2 graph development.

The application is intended to bind to loopback/private network only. Go's
public HTTP/SSE gateway remains the external business entry point.
"""
from __future__ import annotations

import os
from typing import Any

from fastapi import FastAPI, Header, HTTPException, Request
from pydantic import BaseModel, Field

from app.graph.runtime import load_checkpoint_store
from app.graph.service import LangGraphRagService
from app.graph.state import Scope

app = FastAPI(title="EcommerceRAG Python AI Plane", version="0.2.0")
_checkpoint_store = load_checkpoint_store()
_verify_schema = getattr(_checkpoint_store, "verify_schema", None)
if callable(_verify_schema):
    _verify_schema()
_service = LangGraphRagService(checkpoint_store=_checkpoint_store)


class GraphRequest(BaseModel):
    query: str = Field(min_length=1, max_length=4000)
    scope: Scope
    response_purpose: str = "internal"


class GraphResponse(BaseModel):
    answer: str
    evidence: list[dict[str, Any]]
    citations: list[dict[str, Any]]
    can_copy: bool
    is_mock: bool
    trace: dict[str, Any]


def _require_internal(request: Request, token: str | None) -> None:
    configured = os.environ.get("V2_DEV_TOKEN", "")
    host = request.client.host if request.client else ""
    loopback = host in {"127.0.0.1", "::1", "localhost", "testclient"}
    if not loopback:
        raise HTTPException(status_code=403, detail="internal endpoint is not public")
    if configured and token != configured:
        raise HTTPException(status_code=403, detail="internal endpoint token is invalid")


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": "python-ai-plane", "profile": os.environ.get("RAG_PROFILE", "v2_langgraph_fixture")}


@app.get("/ready")
def ready() -> dict[str, Any]:
    return {"status": "ready", "graph": "compiled", "real_service_acceptance": False}


@app.post("/dev/graph", response_model=GraphResponse)
def run_graph(request: Request, payload: GraphRequest, x_internal_token: str | None = Header(default=None)) -> GraphResponse:
    _require_internal(request, x_internal_token)
    try:
        result = _service.run(query=payload.query, scope=payload.scope, response_purpose=payload.response_purpose)
    except PermissionError as error:
        raise HTTPException(status_code=403, detail=str(error)) from error
    except ValueError as error:
        raise HTTPException(status_code=400, detail=str(error)) from error
    return GraphResponse(
        answer=result["answer"], evidence=result.get("evidence", []), citations=result.get("citations", []),
        can_copy=bool(result.get("can_copy", False)), is_mock=bool(result.get("is_mock", True)),
        trace=result.get("trace", {}),
    )
