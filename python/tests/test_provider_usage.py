import logging

from app.providers.usage import UsageRecorder


def test_record_explicit_tenant_and_nulls():
    rows=[]
    recorder=UsageRecorder(sink=rows.append)
    assert recorder.record(tenant_id=7,slot="control",model="m",input_tokens=10,output_tokens=None,http_status=500,error_code="x")
    assert rows==[{"tenant_id":7,"provider_id":None,"slot":"control","model":"m","input_tokens":10,"output_tokens":None,"http_status":500,"error_code":"x"}]

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
    from app.providers.usage import UsageRecorder
    provider.usage=UsageRecorder(sink=rows.append)
    provider._record_usage("control",ProviderUsage(10,2,12),200,None,7)
    provider._record_usage("control",None,500,"upstream_error",7)
    assert len(rows)==2
    assert rows[0]["input_tokens"]==10 and rows[0]["output_tokens"]==2 and rows[0]["tenant_id"]==7
    assert rows[1]["input_tokens"] is None and rows[1]["output_tokens"] is None and rows[1]["error_code"]=="upstream_error"

def test_budget_and_usage_share_same_usage_numbers(tmp_path):
    from app.providers.budget import BudgetLedger, initialize_ledger
    from app.providers.contracts import ProviderUsage
    tmp_path.chmod(0o700); path=tmp_path/"ledger.sqlite"
    initialize_ledger(path,"test","1")
    ledger=BudgetLedger(path,"test","1")
    attempt=ledger.reserve("control","qwen3.8-flash",{"max_tokens":256,"messages":[]},region="cn-beijing")
    usage=ProviderUsage(10,2,12)
    ledger.record(attempt,data={"usage":{"prompt_tokens":10,"completion_tokens":2}},http_status=200)
    rows=[]; UsageRecorder(sink=rows.append).record(tenant_id=9,slot="control",model="qwen3.8-flash",input_tokens=10,output_tokens=2,http_status=200)
    import json
    import sqlite3
    with sqlite3.connect(path) as conn:
        observed,usage_json=conn.execute("SELECT observed_micro,usage_json FROM attempt WHERE id=?",(attempt,)).fetchone()
    assert observed is not None and len(rows)==1
    assert json.loads(usage_json)["prompt_tokens"]==rows[0]["input_tokens"]==10
    assert json.loads(usage_json)["completion_tokens"]==rows[0]["output_tokens"]==2


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
