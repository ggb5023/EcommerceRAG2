import grpc
import pytest

from app.rpc.rag import RagService
from rag.v1 import rag_pb2


class Abort(Exception):
    pass


class Context:
    def abort(self, code, details):
        raise Abort((code, details))

    def is_active(self):
        return True


def scope():
    return rag_pb2.RequestContext(request_id="contract", tenant_id="v2-demo-tenant", user_id="dev-user", role="operator", shop_id="shop-demo", allowed_shop_ids=["shop-demo"], allowed_document_ids=["v2-doc-bottle"], permission_revision="1", enforce_document_scope=True, scope_audience="ai")


def test_final_rpc_adapter_preserves_query_stream_and_facts():
    service = RagService(profile="v2_langchain_mock", scope_validator=lambda _: None)
    context = Context()
    request_scope = scope()
    understood = service.Understand(rag_pb2.UnderstandRequest(context=request_scope, query="保温杯容量是多少？"), context)
    responses = list(service.Search(rag_pb2.SearchRequest(context=request_scope, query=understood.rewritten_query), context))
    evidence = [item for response in responses for item in response.evidence]
    assert responses[-1].complete and evidence[0].document_id == "v2-doc-bottle"
    generated = list(service.Generate(rag_pb2.GenerateRequest(context=request_scope, query=understood.rewritten_query, evidence=evidence), context))
    assert generated[-1].done
    facts = service.Understand(rag_pb2.UnderstandRequest(context=request_scope, query="保温杯价格是多少？"), context)
    assert list(facts.required_fact_tools) == ["get_price"]


def test_final_rpc_rejects_legacy_profile_and_wrong_audience():
    with pytest.raises(ValueError):
        RagService(profile="m1_fixture_mock")
    service = RagService(profile="v2_langgraph_fixture", scope_validator=lambda _: None)
    request_scope = scope()
    request_scope.scope_audience = "ingest"
    with pytest.raises(Abort) as error:
        service.Understand(rag_pb2.UnderstandRequest(context=request_scope, query="test"), Context())
    assert error.value.args[0][0] == grpc.StatusCode.PERMISSION_DENIED
