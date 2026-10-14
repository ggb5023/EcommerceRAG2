import hashlib
import importlib.util
import io
import json
import tarfile
from pathlib import Path

import pytest

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('restore_tool',ROOT/'scripts/selfhost-restore.py')
tool=importlib.util.module_from_spec(spec);spec.loader.exec_module(tool)


def backup(root,name='bootstrap/config.json',kind=tarfile.REGTYPE,mode=0o600):
    for f in ['ecr_v2.dump','keycloak.dump','budget.sqlite']:(root/f).write_bytes(b'fixture')
    with tarfile.open(root/'runtime.tar.gz','w:gz') as tar:
        m=tarfile.TarInfo(name);m.uid=0;m.mode=mode;m.type=kind
        if kind==tarfile.REGTYPE:m.size=7
        else:m.linkname='/etc/passwd'
        tar.addfile(m,io.BytesIO(b'fixture') if m.isfile() else None)
    hashes={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in root.iterdir()}
    (root/'manifest.json').write_text(json.dumps({'hashes':hashes}))


@pytest.mark.parametrize('name,kind,mode',[('../escape',tarfile.REGTYPE,0o600),('/absolute',tarfile.REGTYPE,0o600),('link',tarfile.SYMTYPE,0o600),('hardlink',tarfile.LNKTYPE,0o600),('public-secret',tarfile.REGTYPE,0o644)])
def test_restore_refuses_unsafe_archives_before_mutation(tmp_path,name,kind,mode):
    backup(tmp_path,name,kind,mode)
    with pytest.raises(ValueError,match='unsafe archive'):tool.checked_archive(tmp_path)


def test_restore_verifies_sealed_bytes(tmp_path):
    backup(tmp_path)
    assert tool.checked_archive(tmp_path)
    (tmp_path/'ecr_v2.dump').write_bytes(b'changed')
    with pytest.raises(ValueError,match='integrity'):tool.checked_archive(tmp_path)
