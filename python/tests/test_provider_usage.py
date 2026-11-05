import logging

from app.providers.usage import (
    UsageAttribution,
    UsageRecorder,
    attribution_from_scope,
    use_attribution,
)


def test_record_explicit_tenant_and_nulls():
    rows=[]
    recorder=UsageRecorder(sink=rows.append)
    assert recorder.record(tenant_id=7,slot="control",model="m",input_tokens=10,output_tokens=None,http_status=500,error_code="x")
    assert rows==[{"tenant_id":7,"provider_id":None,"slot":"control","model":"m","input_tokens":10,"output_tokens":None,"http_status":500,"error_code":"x","request_id":None,"execution_id":None,"turn_id":None}]

def test_scope_attribution_is_copied_to_usage_rows():
    rows=[]
    recorder=UsageRecorder(sink=rows.append)
    scope={"request_id":"v2-123","execution_id":"42","turn_id":"7"}
    with use_attribution(attribution_from_scope(scope)):
        assert recorder.record(tenant_id=2,slot="generation",model="m")
    assert rows[0]["request_id"]=="v2-123"
    assert rows[0]["execution_id"]==42 and rows[0]["turn_id"]==7

def test_customer_scope_keeps_text_request_id_and_nulls_numeric_ids():
    attribution=attribution_from_scope({"request_id":"abcdef","execution_id":"abcdef","turn_id":"abcdef"})
    assert attribution==UsageAttribution(request_id="abcdef",execution_id=None,turn_id=None)

def test_attribution_does_not_leak_outside_context():
    rows=[]
    recorder=UsageRecorder(sink=rows.append)
    with use_attribution(UsageAttribution(request_id="v2-1",execution_id=1,turn_id=2)):
        recorder.record(tenant_id=1,slot="control",model="m")
    recorder.record(tenant_id=1,slot="control",model="m")
    assert rows[0]["request_id"]=="v2-1"
    assert rows[1]["request_id"] is None and rows[1]["execution_id"] is None and rows[1]["turn_id"] is None

def test_record_write_failure_is_swallowed_and_warned(caplog):
    def sink(_): raise RuntimeError("db down")
    recorder=UsageRecorder(sink=sink)
    with caplog.at_level(logging.WARNING):
        assert recorder.record(tenant_id=1,slot="embedding",model="m") is False
    assert "usage_log write failed" in caplog.text

def test_provider_double_write_rows_keep_tokens_and_null_failures():
    from types import SimpleNamespace

    from app.providers.aliyun_bailian import AlibabaBailianProvider
    from app.providers.contracts import ProviderUsage
    rows=[]
    provider=object.__new__(AlibabaBailianProvider)
    provider.config=SimpleNamespace(models={"control":"m"})
    from app.providers.usage import (
        UsageRecorder,
    )
    provider.usage=UsageRecorder(sink=rows.append)
    provider._record_usage("control",ProviderUsage(10,2,12),200,None,7)
    provider._record_usage("control",None,500,"upstream_error",7)
    assert len(rows)==2
    assert rows[0]["input_tokens"]==10 and rows[0]["output_tokens"]==2 and rows[0]["tenant_id"]==7
    assert rows[1]["input_tokens"] is None and rows[1]["output_tokens"] is None and rows[1]["error_code"]=="upstream_error"

def test_worker_embedding_writes_one_summed_tenant_row():
    from types import SimpleNamespace

    from app.ingest.worker import IngestWorker
    from app.providers.contracts import EmbeddingResult, ProviderUsage
    rows=[]
    class Embedding:
        async def embed(self,texts,**kwargs):
            assert kwargs["tenant_id"]==42
            return [EmbeddingResult((0.1,)*1024,(), "m",1024,f"r{i}",3,ProviderUsage(3,0,3)) for i,_ in enumerate(texts)]
    worker=object.__new__(IngestWorker); worker.embedding_provider=Embedding(); worker.usage=UsageRecorder(sink=rows.append)
    claim=SimpleNamespace(tenant_id=42)
    parsed={"documents":[{"chunks":[{"content":"a"},{"content":"b"}]}]}
    import asyncio
    asyncio.run(worker._embed(parsed,claim,__import__("threading").Event()))
    assert len(rows)==1 and rows[0]["tenant_id"]==42 and rows[0]["input_tokens"]==6


def test_provider_record_usage_flag_controls_recorder(monkeypatch):
    from app.providers.aliyun_bailian import AlibabaBailianProvider
    from app.providers.config import ProviderConfig

    monkeypatch.setenv("V2_AI_DSN", "postgresql://usage.test/db")
    config = ProviderConfig(
        profile="aliyun-bailian", endpoint="https://provider.test", region="test",
        control_model="control", embedding_model="embedding", rerank_model="rerank",
        generation_model="generation", embedding_dimensions=1024, timeout_s=1,
        quota_rpm=1, api_key="secret",
    )
    assert AlibabaBailianProvider(config, transport=object(), record_usage=True).usage is not None
    assert AlibabaBailianProvider(config, transport=object(), record_usage=False).usage is None


def test_ai_usage_recorder_falls_back_to_retriever_dsn(monkeypatch):
    monkeypatch.delenv("V2_USAGE_DSN", raising=False)
    monkeypatch.delenv("V2_AI_DSN", raising=False)
    monkeypatch.setenv("V2_RETRIEVER_DSN", "postgresql://ai.example/ecr_v2")
    recorder = UsageRecorder.from_environment(role="ai")
    assert recorder is not None and recorder.dsn == "postgresql://ai.example/ecr_v2"
