import re
from pathlib import Path


def test_all_provider_ainvoke_calls_are_scoped():
    source = (Path(__file__).resolve().parents[1] / "app/graph/workflow.py").read_text()
    calls = len(re.findall(r"chains\.(control|embedding|rerank|generation)\.ainvoke\(", source))
    wrapped = len(re.findall(r"chains\.(control|embedding|rerank|generation)\.ainvoke\(scoped\(state,", source))
    assert calls > 0 and calls == wrapped
