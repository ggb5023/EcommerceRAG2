import json
from dataclasses import replace

import grpc
import pytest

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


def test_final_rpc_rejects_legacy_profile_and_wrong_audience():
    with pytest.raises(ValueError):
        RagService(profile="m1_fixture_mock")
    service = RagService(profile="v2_langgraph_fixture", scope_validator=lambda _: None)
    request_scope = scope()
    request_scope.scope_audience = "ingest"
    with pytest.raises(Abort) as error:
        service.Understand(rag_pb2.UnderstandRequest(context=request_scope, query="test"), Context())
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
        facts=[rag_pb2.Fact(tool_name="get_logistics", value_json='{"status":"in_transit"}',
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
