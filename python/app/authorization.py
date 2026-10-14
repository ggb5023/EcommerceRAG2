"""Validate signed scopes with the fixed Go authority; never issue grants."""
from __future__ import annotations

import ipaddress
import json
import os
import urllib.error
import urllib.parse
import urllib.request
from functools import lru_cache

import grpc

from app.rpc.transport import channel, enabled
from rag.v1 import rag_pb2, rag_pb2_grpc


@lru_cache(maxsize=8)
def _authority(address):
    return rag_pb2_grpc.AuthorizationServiceStub(channel(address))


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def validate_scope(scope) -> None:
    address = os.environ.get('V2_AUTHORITY_GRPC_ADDR', '')
    if address:
        if not enabled():
            raise ScopeError(grpc.StatusCode.UNAVAILABLE)
        try:
            result = _authority(address).ValidateScope(rag_pb2.ValidateScopeRequest(context=scope), timeout=2)
            if not result.allowed:
                raise ScopeError(grpc.StatusCode.PERMISSION_DENIED)
            return
        except grpc.RpcError as error:
            raise ScopeError(grpc.StatusCode.PERMISSION_DENIED if error.code() in {
                grpc.StatusCode.PERMISSION_DENIED, grpc.StatusCode.UNAUTHENTICATED}
                else grpc.StatusCode.UNAVAILABLE) from None
    if enabled():
        raise ScopeError(grpc.StatusCode.UNAVAILABLE)
    base = os.environ.get("AUTHORITY_HTTP_BASE", "http://127.0.0.1:8080")
    parsed = urllib.parse.urlsplit(base)
    try:
        loopback = ipaddress.ip_address(parsed.hostname or "").is_loopback
    except ValueError:
        loopback = False
    if (not loopback or parsed.scheme != "http" or parsed.username or parsed.password
            or parsed.path not in {"", "/"} or parsed.query or parsed.fragment):
        raise ScopeError(grpc.StatusCode.UNAVAILABLE)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), _NoRedirect())
    request = urllib.request.Request(base.rstrip("/") + "/internal/authorization/validate",
                                     data=scope.SerializeToString(deterministic=True),
                                     headers={"Content-Type": "application/x-protobuf"}, method="POST")
    try:
        with opener.open(request, timeout=2) as response:
            if response.status != 200 or json.loads(response.read(4096)) != {"allowed": True}:
                raise ScopeError(grpc.StatusCode.PERMISSION_DENIED)
    except urllib.error.HTTPError as error:
        raise ScopeError(grpc.StatusCode.PERMISSION_DENIED if error.code in {400, 401, 403}
                         else grpc.StatusCode.UNAVAILABLE) from None
    except (OSError, ValueError):
        raise ScopeError(grpc.StatusCode.UNAVAILABLE) from None


class ScopeError(Exception):
    def __init__(self, code):
        self.code = code
