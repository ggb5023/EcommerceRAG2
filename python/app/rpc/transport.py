"""Mutually authenticated internal RPC; no public insecure bind fallback."""
from __future__ import annotations

import os
from pathlib import Path

import grpc


def enabled() -> bool:
    mode = os.environ.get('V2_RPC_TLS', '')
    if mode not in {'', 'mtls'}:
        raise ValueError('unsupported internal RPC transport')
    return mode == 'mtls'


def material() -> tuple[bytes, bytes, bytes]:
    if not enabled():
        raise ValueError('mTLS is required')
    key = Path(os.environ['V2_RPC_KEY_FILE'])
    if not key.is_file() or key.is_symlink() or key.stat().st_mode & 0o077 or key.stat().st_uid != os.geteuid():
        raise ValueError('RPC key must be an owned protected regular file')
    return (key.read_bytes(), Path(os.environ['V2_RPC_CERT_FILE']).read_bytes(),
            Path(os.environ['V2_RPC_CA_FILE']).read_bytes())


def channel(address: str):
    key, cert, ca = material()
    return grpc.secure_channel(address, grpc.ssl_channel_credentials(ca, key, cert))


def server_credentials():
    key, cert, ca = material()
    return grpc.ssl_server_credentials(((key, cert),), root_certificates=ca, require_client_auth=True)
