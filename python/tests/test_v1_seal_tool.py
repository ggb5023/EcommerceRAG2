"""Legacy target binding must fail closed before invoking remote operations."""
import importlib.util
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("v1_seal_tool", ROOT / "scripts/seal-v1-database.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


def test_exact_legacy_binding_and_restricted_permissions(tmp_path):
    config = tmp_path / "legacy.env"
    config.write_text("PGDATABASE=rag\nPGUSER=rag_app\nPGPASSWORD=synthetic-value\n")
    config.chmod(0o600)
    assert module.legacy_binding(config)
    config.chmod(0o644)
    assert not module.legacy_binding(config)
    config.chmod(0o600)
    config.write_text("PGDATABASE=ecr_v2_test_fixture\nPGUSER=rag_app\n")
    assert not module.legacy_binding(config)
    config.write_text("PGDATABASE=rag\nPGUSER=postgres\n")
    assert not module.legacy_binding(config)


def test_symlink_cannot_redirect_target(tmp_path):
    config = tmp_path / "legacy.env"
    config.write_text("PGDATABASE=rag\nPGUSER=rag_app\n")
    config.chmod(0o600)
    link = tmp_path / "alias.env"
    link.symlink_to(config)
    assert not module.legacy_binding(link)
