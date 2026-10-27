import asyncio
import tempfile
from pathlib import Path
from threading import Event

from app.chains.provider import build_provider_chains
from app.graph.runtime import load_provider_service
from app.graph.service import LangGraphRagService
from app.providers.aliyun_bailian import MockProvider
from app.providers.config import validate_values


def mock_provider():
    values = {
        "PROVIDER_PROFILE": "mock",
        "CONTROL_MODEL": "mock-control",
        "EMBEDDING_MODEL": "mock-embedding",
        "RERANK_MODEL": "mock-rerank",
        "GENERATION_MODEL": "mock-generation",
    }
    return MockProvider(validate_values(values))


def scope(*documents):
    return {
        "tenant_id": "v2-demo-tenant",
        "user_id": "dev-user",
        "permission_revision": "1",
        "role": "operator",
        "allowed_shop_ids": ["shop-demo"],
        "allowed_document_ids": list(documents),
        "shop_id": "shop-demo",
    }


async def _provider_chains_keep_metadata_and_embedding_contract():
    chains = build_provider_chains(
        control=mock_provider(),
        embedding=mock_provider(),
        rerank=mock_provider(),
        generation=mock_provider(),
    )
    control = await chains.control.ainvoke({
        "tenant_id": 1, "shop_id": "test-shop",
        "messages": [{"role": "user", "content": "问题"}],
    })
    assert control["provider"]["request_id"].startswith("mock-control-")
    embedding = await chains.embedding.ainvoke({
        "tenant_id": 1, "shop_id": "test-shop",
        "texts": ["文本"], "text_type": "document", "output_type": "dense&sparse",
    })
    assert len(embedding["items"][0]["dense"]) == 1024
    assert embedding["items"][0]["provider"]["model"] == "mock-embedding"


async def _provider_graph_is_async_and_remains_non_copyable():
    provider = mock_provider()
    chains = build_provider_chains(
        control=provider, embedding=provider, rerank=provider, generation=provider,
    )
    result = await LangGraphRagService().arun(
        query="保温杯容量是多少？",
        scope=scope("v2-doc-bottle"),
        response_purpose="customer_reply",
        provider_chains=chains,
    )
    assert result["is_mock"] is False
    assert result["can_copy"] is False
    assert result["evidence"][0]["document_id"] == "v2-doc-bottle"
    assert result["trace"]["provider"]["request_id"].startswith("mock-generation-")
    assert result["trace"]["retrieval"]["retrieval"]["retrieval_mode"] == "fixture_embedding_contract"


def test_provider_chains_keep_metadata_and_embedding_contract():
    asyncio.run(_provider_chains_keep_metadata_and_embedding_contract())


def test_provider_graph_is_async_and_remains_non_copyable():
    asyncio.run(_provider_graph_is_async_and_remains_non_copyable())


def test_mock_provider_control_preserves_vague_query_clarification_contract():
    async def run():
        provider = mock_provider()
        chains = build_provider_chains(
            control=provider, embedding=provider, rerank=provider, generation=provider,
        )
        result = await LangGraphRagService().arun(
            query="那个怎么样？", scope=scope("v2-doc-bottle"),
            response_purpose="customer_reply", provider_chains=chains,
        )
        assert result["intent"] == "clarification"
        assert result["confidence"] < 0.5
        assert result["clarification"]

    asyncio.run(run())


def test_public_freshness_requests_retrieve_limits_but_prior_ambiguity_still_clarifies():
    import json
    import types

    from langchain_core.runnables import RunnableLambda

    from app.graph.workflow import provider_understand_node

    requests = []

    async def control(value):
        requests.append(value)
        return {'structured': {'rewritten_query': '原查询', 'intent': 'knowledge',
            'information_source': 'knowledge', 'required_fact_tools': [],
            'clarification': '', 'confidence': .8, 'entities': []}}

    chains = types.SimpleNamespace(control=RunnableLambda(lambda _: None, afunc=control))
    asyncio.run(provider_understand_node({'query': '今天公开新闻里这类家电有什么召回消息？'}, chains))
    fields = requests[-1]['response_schema']['properties']
    assert fields['intent']['enum'] == ['knowledge']
    assert fields['required_fact_tools']['maxItems'] == 0
    assert '抓取记录' in requests[-1]['messages'][0]['content']
    # A real unresolved reference remains a separate clarification requirement.
    asyncio.run(provider_understand_node({'query': '沿用上一问的标准，哪条是最新？',
        'history_summary': json.dumps([{'role': 'user', 'content': '比较USB-C和USB-A标准'}])}, chains))
    assert requests[-1]['response_schema']['properties']['intent']['enum'] != ['knowledge']


def test_workflow_context_preserves_reranked_prerequisites_without_widening_disclosure():
    import types

    from app.graph.workflow import provider_context_gate_node

    evidence = [dict(id=f'chunk-{i}', document_id=f'doc-{i}', content='supplied body',
                     disclosure_class='external_allowed', customer_eligible=True) for i in range(10)]
    private = dict(id='private', document_id='private', content='private body',
                   disclosure_class='internal_only', customer_eligible=False)
    async def select(_):
        raise AssertionError('workflow context must preserve its authorized ranked prerequisites')
    from langchain_core.runnables import RunnableLambda
    chains = types.SimpleNamespace(control=RunnableLambda(lambda _: None, afunc=select))
    result = asyncio.run(provider_context_gate_node(dict(query='时效要求不明确时应先澄清什么？',
        evidence=[private, *evidence], response_purpose='customer_reply'), chains))
    assert result['evidence'] == evidence[:8]


def test_provider_graph_calls_embedding_with_query_and_document_contracts():
    class RecordingProvider(MockProvider):
        def __init__(self, config):
            super().__init__(config)
            self.calls = []

        async def embed(self, texts, *, text_type, dimensions=1024, output_type="dense&sparse", instruct_version="", cancel_event=None, tenant_id=None):
            self.calls.append((list(texts), text_type, dimensions, output_type))
            return await super().embed(
                texts,
                text_type=text_type,
                dimensions=dimensions,
                output_type=output_type,
                instruct_version=instruct_version,
                cancel_event=cancel_event,
                tenant_id=tenant_id,
            )

    async def run():
        provider = RecordingProvider(validate_values({
            "PROVIDER_PROFILE": "mock",
            "CONTROL_MODEL": "mock-control",
            "EMBEDDING_MODEL": "mock-embedding",
            "RERANK_MODEL": "mock-rerank",
            "GENERATION_MODEL": "mock-generation",
        }))
        chains = build_provider_chains(
            control=provider, embedding=provider, rerank=provider, generation=provider,
        )
        result = await LangGraphRagService().arun(
            query="保温杯容量是多少？",
            scope=scope("v2-doc-bottle"),
            response_purpose="customer_reply",
            provider_chains=chains,
        )
        assert result["evidence"][0]["document_id"] == "v2-doc-bottle"
        assert [call[1:] for call in provider.calls] == [
            ("query", 1024, "dense&sparse"),
            ("document", 1024, "dense&sparse"),
        ]

    asyncio.run(run())


def test_mock_reranker_scores_chinese_terms_instead_of_truncating_by_position():
    async def run():
        provider = mock_provider()
        result = await provider.rerank(
            "配送需要几个工作日？",
            ["晴野收纳箱颜色和尺寸", "标准配送通常需要 2-5 个工作日"],
            top_n=1,
        )
        assert result.items[0].index == 1

    asyncio.run(run())


def test_provider_graph_uses_persistent_retriever_without_fixture_candidates():
    class RecordingRetriever:
        def __init__(self):
            self.calls = []

        async def asearch(self, **kwargs):
            self.calls.append(kwargs)
            return [{
                "id": "db-chunk-1",
                "content": "持久化资料中的保温杯容量为480毫升。",
                "source_type": "postgres_chunk",
                "source_ref": "db://chunk/1",
                "document_id": "persist-doc",
                "version_id": "persist-version",
                "tenant_id": "v2-demo-tenant",
                "shop_id": "shop-demo",
                "disclosure_class": "external_allowed",
                "customer_eligible": True,
                "raw_score": 1.0,
                "dense_rank": 1,
                "sparse_rank": 1,
                "metadata": {},
            }]

    async def run():
        provider = mock_provider()
        retriever = RecordingRetriever()
        chains = build_provider_chains(
            control=provider, embedding=provider, rerank=provider, generation=provider,
            retriever=retriever,
        )
        result = await LangGraphRagService().arun(
            query="保温杯容量是多少？",
            scope=scope("persist-doc"),
            response_purpose="customer_reply",
            provider_chains=chains,
        )
        assert result["evidence"][0]["document_id"] == "persist-doc"
        assert result["trace"]["retrieval"]["retrieval"]["retrieval_mode"] == "pgvector_trigram_rrf"
        assert len(retriever.calls) == 1
        assert retriever.calls[0]["scope"]["allowed_document_ids"] == ["persist-doc"]
        assert retriever.calls[0]["limit"] == 20

    asyncio.run(run())


def test_provider_generate_preserves_go_seed_evidence_order_and_fields():
    async def run():
        provider = mock_provider()
        chains = build_provider_chains(
            control=provider, embedding=provider, rerank=provider, generation=provider,
        )
        evidence = [
            {
                "id": "seed-a", "content": "第一条已授权证据", "source_type": "postgres_chunk",
                "source_ref": "db://seed/a", "document_id": "seed-a", "version_id": "version-a",
                "tenant_id": "v2-demo-tenant", "shop_id": "shop-demo",
                "disclosure_class": "external_allowed", "customer_eligible": True,
                "rank": 1, "raw_score": 0.1,
            },
            {
                "id": "seed-b", "content": "第二条已授权证据", "source_type": "postgres_chunk",
                "source_ref": "db://seed/b", "document_id": "seed-b", "version_id": "version-b",
                "tenant_id": "v2-demo-tenant", "shop_id": "shop-demo",
                "disclosure_class": "external_allowed", "customer_eligible": True,
                "rank": 2, "raw_score": 0.9,
            },
        ]
        result = await LangGraphRagService().arun_with_evidence(
            query="问题", scope=scope("seed-a", "seed-b"), evidence=evidence,
            response_purpose="customer_reply", provider_chains=chains,
        )
        assert [item["id"] for item in result["citations"]] == ["seed-a", "seed-b"]
        assert result["citations"] == evidence
        assert result["answer"]

    asyncio.run(run())


def test_generated_qualification_citations_are_merged_and_unsupplied_ids_rejected():
    import types

    import pytest
    from langchain_core.runnables import RunnableLambda

    from app.graph.workflow import provider_generate_node

    evidence = [dict(id=name, document_id=name, version_id=name, content=text)
        for name, text in [('main', '标称容量480ml。'), ('limit', '容量不代表实际饮用量。')]]
    structured = {'answer': '标称容量480ml。', 'citation_ids': ['main'],
        'evidence_limits': '容量不代表实际饮用量。', 'next_inputs': '', 'limit_citation_ids': ['limit']}

    async def generate(_):
        return {'structured': structured}

    chains = types.SimpleNamespace(generation=RunnableLambda(lambda _: None, afunc=generate))
    result = asyncio.run(provider_generate_node({'query': '容量是多少？', 'evidence': evidence}, chains))
    assert [e['id'] for e in result['citations']] == ['main', 'limit']
    for ids in ([], ['unknown'], ['limit', 'limit']):
        structured['limit_citation_ids'] = ids
        with pytest.raises(ValueError, match='limits lack cited support'):
            asyncio.run(provider_generate_node({'query': '容量是多少？', 'evidence': evidence}, chains))


def test_provider_graph_propagates_deadline_and_cancellation_boundary():
    class RecordingProvider(MockProvider):
        def __init__(self, config):
            super().__init__(config)
            self.seen = None

        async def control(self, messages, *, response_schema=None, timeout_s=None, cancel_event=None):
            self.seen = (timeout_s, cancel_event)
            return await super().control(
                messages,
                response_schema=response_schema,
                timeout_s=timeout_s,
                cancel_event=cancel_event,
            )

    async def run():
        provider = RecordingProvider(validate_values({
            "PROVIDER_PROFILE": "mock",
            "CONTROL_MODEL": "mock-control",
            "EMBEDDING_MODEL": "mock-embedding",
            "RERANK_MODEL": "mock-rerank",
            "GENERATION_MODEL": "mock-generation",
        }))
        chains = build_provider_chains(
            control=provider, embedding=provider, rerank=provider, generation=provider,
        )
        event = Event()
        result = await LangGraphRagService().arun(
            query="保温杯容量是多少？", scope=scope("v2-doc-bottle"),
            provider_chains=chains, cancel_event=event, timeout_s=1.25,
        )
        assert result["answer"]
        assert provider.seen == (1.25, event)
        event.set()
        try:
            await LangGraphRagService().arun(
                query="保温杯容量是多少？", scope=scope("v2-doc-bottle"),
                provider_chains=chains, cancel_event=event, timeout_s=1.25,
            )
        except RuntimeError as error:
            assert str(error) == "request cancelled"
        else:
            raise AssertionError("cancelled graph execution must fail closed")

    asyncio.run(run())


def test_provider_runtime_factory_keeps_profile_selection_explicit():
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / "providers.env"
        path.write_text(
            "PROVIDER_PROFILE=mock\n"
            "CONTROL_MODEL=mock-control\n"
            "EMBEDDING_MODEL=mock-embedding\n"
            "RERANK_MODEL=mock-rerank\n"
            "GENERATION_MODEL=mock-generation\n",
            encoding="utf-8",
        )
        service = load_provider_service(path)
    assert service.provider_chains is not None


def test_context_selection_only_narrows_authorized_sources_and_skips_signed_generation():
    from app.graph.workflow import provider_context_gate_node
    from langchain_core.runnables import RunnableLambda
    import pytest
    import types
    evidence = [dict(id='source-news', document_id='news', content='News source conflict guidance', rerank_score=.95,
                     disclosure_class='external_allowed', customer_eligible=True),
                dict(id='source-order', document_id='order', content='Order refund policy conflict', rerank_score=.8,
                     disclosure_class='external_allowed', customer_eligible=True)]
    state = dict(query='News conflict?', evidence=evidence, response_purpose='customer_reply')
    async def run():
        async def select(_): return {'structured': {'selected_ids': ['source-news']}}
        chains = types.SimpleNamespace(control=RunnableLambda(lambda _: None, afunc=select))
        result = await provider_context_gate_node(state, chains)
        assert result['evidence'] == evidence[:1]
        assert (await provider_context_gate_node(dict(state, seed_evidence=evidence), chains))['evidence'] == evidence
        async def widen(_): return {'structured': {'selected_ids': ['unauthorized']}}
        chains.control = RunnableLambda(lambda _: None, afunc=widen)
        with pytest.raises(ValueError, match='outside supplied'):
            await provider_context_gate_node(state, chains)
    asyncio.run(run())
