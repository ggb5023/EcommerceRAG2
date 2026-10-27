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
