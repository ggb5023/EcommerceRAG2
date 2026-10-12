import json
from dataclasses import replace

import grpc
import pytest
from app.authorization import ScopeError
from app.chains.provider import build_provider_chains
from app.graph.fixtures import FIXTURE_CORPUS
from app.graph.service import LangGraphRagService
from app.providers import MockProvider
from app.providers.config import validate_values
from app.providers.contracts import ProviderUsage
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


@pytest.mark.parametrize("profile", ["v2_langchain_mock", "v2_langgraph_fixture"])
def test_order_routes_extract_only_explicit_order_resource_ids(profile):
    service = RagService(profile=profile, scope_validator=lambda _: None)
    explicit = service.Understand(
        rag_pb2.UnderstandRequest(context=scope(), query="查询订单号 V2-ORDER-001 的物流状态"), Context(),
    )
    assert list(explicit.required_fact_tools) == ["get_order_status", "get_logistics"]
    assert [(item.name, item.value) for item in explicit.entities] == [("order_id", "V2-ORDER-001")]
    missing = service.Understand(
        rag_pb2.UnderstandRequest(context=scope(), query="订单状态？"), Context(),
    )
    assert list(missing.required_fact_tools) == ["get_order_status"]
    assert not missing.entities


@pytest.mark.parametrize("field", ["confidence", "entities"])
@pytest.mark.parametrize("value", [float("nan"), float("inf"), True])
def test_control_rejects_nonfinite_or_boolean_confidence(field, value):
    from app.graph.workflow import _provider_understand_result

    structured = {"confidence": 0.9, "entities": []}
    structured[field] = value if field == "confidence" else [{"name": "order_id", "value": "ORDER-1", "confidence": value}]
    with pytest.raises(TypeError):
        _provider_understand_result({"structured": structured}, "订单号 ORDER-1")


def test_final_rpc_rejects_legacy_profile_and_wrong_audience():
    with pytest.raises(ValueError):
        RagService(profile="m1_fixture_mock")
    service = RagService(profile="v2_langgraph_fixture", scope_validator=lambda _: None)
    request_scope = scope()
    request_scope.scope_audience = "ingest"
    with pytest.raises(Abort) as error:
        service.Understand(rag_pb2.UnderstandRequest(context=request_scope, query="test"), Context())
    assert error.value.args[0][0] == grpc.StatusCode.PERMISSION_DENIED


@pytest.mark.parametrize("field,value", [
    ("tenant_id", ""), ("user_id", ""), ("request_id", ""),
    ("permission_revision", ""), ("shop_id", "outside"),
    ("allowed_shop_ids", []), ("all_shops", True), ("enforce_document_scope", False),
])
def test_final_rpc_rejects_incomplete_or_widened_identity(field, value):
    identity = scope()
    if field == "allowed_shop_ids":
        del identity.allowed_shop_ids[:]
    else:
        setattr(identity, field, value)
    service = RagService(profile="v2_langgraph_fixture", scope_validator=lambda _: None)
    with pytest.raises(Abort) as error:
        list(service.Search(rag_pb2.SearchRequest(context=identity, query="保温杯"), Context()))
    assert error.value.args[0][0] == grpc.StatusCode.PERMISSION_DENIED


def test_final_rpc_rechecks_authority_between_generated_events():
    service = RagService(profile="v2_langgraph_fixture", scope_validator=lambda _: None)
    evidence = [item for response in service.Search(rag_pb2.SearchRequest(context=scope(), query="保温杯"), Context()) for item in response.evidence]
    assert evidence
    revoked = False

    def authority(_scope):
        if revoked:
            raise ScopeError(grpc.StatusCode.PERMISSION_DENIED)

    service._scope_validator = authority
    stream = service.Generate(rag_pb2.GenerateRequest(context=scope(), query="保温杯", evidence=evidence, response_purpose="customer_reply"), Context())
    assert next(stream).WhichOneof("event") == "delta"
    revoked = True
    with pytest.raises(Abort) as error:
        next(stream)
    assert error.value.args[0][0] == grpc.StatusCode.PERMISSION_DENIED


def test_cancelled_rpc_stops_before_generating_output():
    class Cancelled(Context):
        def is_active(self):
            return False

    service = RagService(profile="v2_langgraph_fixture", scope_validator=lambda _: None)
    with pytest.raises(Abort) as error:
        list(service.Generate(rag_pb2.GenerateRequest(context=scope(), query="保温杯"), Cancelled()))
    assert error.value.args[0][0] == grpc.StatusCode.CANCELLED


@pytest.mark.parametrize("field,value", [
    ("tenant_id", "other-tenant"), ("shop_id", "other-shop"),
    ("document_id", "other-document"), ("disclosure_class", "internal_only"),
    ("disclosure_class", "unclassified"), ("customer_eligible", False),
])
def test_final_rpc_rejects_out_of_scope_or_ineligible_generation_evidence(field, value):
    service = RagService(profile="v2_langgraph_fixture", scope_validator=lambda _: None)
    evidence = [item for response in service.Search(rag_pb2.SearchRequest(context=scope(), query="保温杯"), Context()) for item in response.evidence]
    assert evidence
    setattr(evidence[0], field, value)
    with pytest.raises(Abort) as error:
        list(service.Generate(rag_pb2.GenerateRequest(context=scope(), query="保温杯", evidence=evidence, response_purpose="customer_reply"), Context()))
    assert error.value.args[0][0] == grpc.StatusCode.PERMISSION_DENIED


def test_rpc_phases_call_only_their_provider_slots_and_preserve_logistics_facts():
    calls = []

    class RecordingProvider(MockProvider):
        async def control(self, messages, **kwargs):
            calls.append("control")
            return await super().control(messages, **kwargs)

        async def embed(self, texts, **kwargs):
            calls.append("embedding:" + kwargs["text_type"])
            return await super().embed(texts, **kwargs)

        async def rerank(self, query, candidates, **kwargs):
            calls.append("rerank")
            return await super().rerank(query, candidates, **kwargs)

        async def generate(self, messages, **kwargs):
            calls.append("generation")
            assert "get_logistics" in messages[-1]["content"]
            return replace(await super().generate(messages, **kwargs), usage=ProviderUsage(3, 5, 8))

    class Retriever:
        async def asearch(self, **kwargs):
            item = next(item for item in FIXTURE_CORPUS if item["document_id"] == "v2-doc-bottle")
            return [dict(item, dense_rank=1, sparse_rank=1, rrf_score=1.7 / 61)]

    provider = RecordingProvider(validate_values({
        "PROVIDER_PROFILE": "mock", "CONTROL_MODEL": "mock-control",
        "EMBEDDING_MODEL": "mock-embedding", "RERANK_MODEL": "mock-rerank",
        "GENERATION_MODEL": "mock-generation",
    }))
    service = RagService(profile="v2_langchain_mock", scope_validator=lambda _: None)
    service._provider_v2 = LangGraphRagService(provider_chains=build_provider_chains(
        control=provider, embedding=provider, rerank=provider, generation=provider,
        retriever=Retriever(),
    ))
    ctx = Context()
    request_scope = scope()
    understood = service.Understand(rag_pb2.UnderstandRequest(context=request_scope, query="保温杯物流状态？"), ctx)
    assert list(understood.required_fact_tools) == ["get_logistics"]
    assert calls == ["control"]
    responses = list(service.Search(rag_pb2.SearchRequest(context=request_scope, query=understood.rewritten_query,
                                                        response_purpose="customer_reply"), ctx))
    evidence = [item for response in responses for item in response.evidence]
    assert calls == ["control", "embedding:query", "rerank"]
    generated = list(service.Generate(rag_pb2.GenerateRequest(
        context=request_scope, query=understood.rewritten_query, evidence=evidence,
        facts=[rag_pb2.Fact(tool_name="get_logistics", resource_id="V2-ORDER-001", value_json='{"order_id":"V2-ORDER-001","status":"in_transit"}',
                           observed_at="2026-10-12T00:00:00Z", is_mock=True)],
        response_purpose="customer_reply",
    ), ctx))
    assert calls == ["control", "embedding:query", "rerank", "generation"]
    assert [event.citation for event in generated if event.WhichOneof("event") == "citation"] == evidence
    usage = next(json.loads(event.usage_json) for event in generated if event.WhichOneof("event") == "usage_json")
    assert usage["provider"]["request_id"].startswith("mock-generation-")
    assert usage["provider"]["usage"] == {"input_tokens": 3, "output_tokens": 5, "total_tokens": 8}
    assert generated[-1].done


@pytest.mark.parametrize("profile", ["v2_langchain_mock", "v2_langgraph_fixture"])
@pytest.mark.parametrize("query,tools", [
    ("商品库存？", ["get_stock"]),
    ("价格和库存？", ["get_price", "get_stock"]),
    ("库存和价格？", ["get_stock", "get_price"]),
    ("订单状态？", ["get_order_status"]),
    ("物流状态？", ["get_logistics"]),
    ("对比商品", ["compare_products"]),
])
def test_development_profiles_route_all_go_fact_tools(profile, query, tools):
    response = RagService(profile=profile, scope_validator=lambda _: None).Understand(
        rag_pb2.UnderstandRequest(context=scope(), query=query), Context(),
    )
    assert list(response.required_fact_tools) == tools
    assert response.intent == "factual"
    assert response.information_source == "realtime_business"


def test_provider_understanding_validates_order_entity_shape():
    from app.graph.workflow import _provider_understand_result

    result = _provider_understand_result({"structured": {
        "rewritten_query": "查询订单号 V2-ORDER-001 的物流状态",
        "intent": "factual", "information_source": "realtime_business",
        "required_fact_tools": ["get_logistics"], "clarification": "", "confidence": 0.9,
        "entities": [{"name": "order_id", "value": "V2-ORDER-001", "confidence": 0.99}],
    }}, "查询订单号 V2-ORDER-001 的物流状态")
    assert result["entities"] == [{"name": "order_id", "value": "V2-ORDER-001", "confidence": 0.99}]
