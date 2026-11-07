import importlib.util
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('backup_tool',ROOT/'scripts/ops/selfhost-backup.py')
tool=importlib.util.module_from_spec(spec);spec.loader.exec_module(tool)


def test_inventory_skips_absent_ingest_and_worker_env(tmp_path):
    for name in ('compose.env','gateway.env','ai.env','identity.env'):
        (tmp_path/name).write_text('fixture\n')
    for name in ('bootstrap','gateway','ai','identity','web','artifacts'):
        (tmp_path/name).mkdir()
    assert tool.archive_inventory(tmp_path)==['compose.env','gateway.env','ai.env','identity.env',
                                              'bootstrap','gateway','ai','identity','web','artifacts']


def test_inventory_keeps_separate_worker_runtime(tmp_path):
    for name in ('compose.env','ingest.env','worker.env'):
        (tmp_path/name).write_text('fixture\n')
    inventory=tool.archive_inventory(tmp_path)
    assert 'ingest.env' in inventory and 'worker.env' in inventory
