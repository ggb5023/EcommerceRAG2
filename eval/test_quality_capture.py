import hashlib
import importlib.util
import json
import os
from pathlib import Path

import pytest

SPEC = importlib.util.spec_from_file_location('quality_capture', Path(__file__).with_name('quality_capture.py'))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def test_capture_permissions_hash_and_immutability(tmp_path):
    directory = tmp_path / 'private'
    first = MODULE.capture(directory, {'case_id': 'case', 'text': 'synthetic answer'})
    second = MODULE.capture(directory, {'case_id': 'case', 'text': 'retry answer'})
    path = directory / first['capture_id']
    assert first['capture_id'] != second['capture_id']
    assert path.stat().st_mode & 0o777 == 0o600
    assert directory.stat().st_mode & 0o777 == 0o700
    assert hashlib.sha256(path.read_bytes()).hexdigest() == first['capture_sha256']
    assert json.loads(path.read_text())['text'] == 'synthetic answer'


def test_capture_rejects_repository_world_readable_and_symlink(tmp_path):
    with pytest.raises(ValueError, match='outside_repository'):
        MODULE.secure_directory(MODULE.ROOT / '.local' / 'answers')
    directory = tmp_path / 'public'
    directory.mkdir(mode=0o755)
    with pytest.raises(ValueError, match='0700'):
        MODULE.capture(directory, {})
    private = tmp_path / 'private'
    private.mkdir(mode=0o700)
    link = tmp_path / 'link'
    os.symlink(private, link)
    with pytest.raises(ValueError, match='symlink'):
        MODULE.capture(link, {})
