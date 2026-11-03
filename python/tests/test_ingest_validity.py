from types import SimpleNamespace

import pytest

from app.ingest.commands import CommandStore, command_action
from app.ingest.database import IngestConflictError
from rag.v1 import rag_pb2


def test_validity_action_and_dates():
    assert command_action(rag_pb2.INGEST_COMMAND_ACTION_VALIDITY) == "version.validity"
    assert CommandStore._validity_dates(SimpleNamespace(effective_from="", effective_to="")) == (None, None)
    assert CommandStore._validity_dates(SimpleNamespace(effective_from="2026-01-01", effective_to="2026-01-02"))[1].isoformat() == "2026-01-02"
    with pytest.raises(IngestConflictError):
        CommandStore._validity_dates(SimpleNamespace(effective_from="2026-01-02", effective_to="2026-01-01"))
    with pytest.raises(IngestConflictError):
        CommandStore._validity_dates(SimpleNamespace(effective_from="2026-02-30", effective_to=""))
