import hashlib

import pytest

from app.configuration import verify_database


def connection(monkeypatch,recorded,*,role='rag_v2_ai',superuser=False,bypassrls=False):
    class Result:
        def fetchone(self):return role,superuser,bypassrls
        def fetchall(self):return recorded
    class Connection:
        def __enter__(self):return self
        def __exit__(self,*args):pass
        def execute(self,*args):return Result()
    monkeypatch.setattr('app.configuration.psycopg.connect',lambda _:Connection())


def test_image_migration_directory_is_checked_and_cannot_skip(monkeypatch,tmp_path):
    file=tmp_path/'0001_test.sql';file.write_text('frozen migration')
    connection(monkeypatch,[('0001',hashlib.sha256(file.read_bytes()).hexdigest())])
    monkeypatch.setenv('V2_MIGRATION_DIR',str(tmp_path))
    verify_database('test','rag_v2_ai')
    file.write_text('changed migration')
    with pytest.raises(ValueError,match='checksums'):verify_database('test','rag_v2_ai')
    file.unlink()
    with pytest.raises(ValueError,match='missing'):verify_database('test','rag_v2_ai')


@pytest.mark.parametrize('superuser,bypassrls',[(True,False),(False,True)])
def test_superuser_and_bypassrls_roles_are_rejected(monkeypatch,tmp_path,superuser,bypassrls):
    file=tmp_path/'0001_test.sql';file.write_text('frozen migration')
    connection(monkeypatch,[('0001',hashlib.sha256(file.read_bytes()).hexdigest())],
               superuser=superuser,bypassrls=bypassrls)
    monkeypatch.setenv('V2_MIGRATION_DIR',str(tmp_path))
    with pytest.raises(ValueError,match='ownership'):verify_database('test','rag_v2_ai')
