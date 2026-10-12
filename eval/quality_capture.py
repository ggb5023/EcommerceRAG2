"""Restricted, immutable answer capture for synthetic engineering review."""
from __future__ import annotations

import hashlib
import json
import os
import stat
from pathlib import Path
from typing import Any
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[1]


def secure_directory(path: Path) -> Path:
    if not path.is_absolute() or path.is_relative_to(ROOT):
        raise ValueError("capture_directory_must_be_outside_repository")
    for parent in (path, *path.parents):
        if parent.is_symlink():
            raise ValueError("capture_directory_symlink")
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = path.stat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise ValueError("capture_directory_requires_owned_0700")
    return path


def capture(directory: Path, payload: dict[str, Any]) -> dict[str, str]:
    directory = secure_directory(directory)
    name = uuid4().hex + ".json"
    body = (json.dumps(payload, ensure_ascii=False, sort_keys=True) + "\n").encode()
    fd = os.open(directory / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(body)
        stream.flush()
        os.fsync(stream.fileno())
    return {"capture_id": name, "capture_sha256": hashlib.sha256(body).hexdigest()}
