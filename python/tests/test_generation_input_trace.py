import asyncio
import hashlib
import json
from types import SimpleNamespace

import pytest
from app.graph.workflow import provider_generate_node
from app.retrieval.postgres import PostgresRetriever


def test_generation_trace_is_final_constructed_input_even_when_provider_fails():
    records = []
    class Recorder:
        async def arecord_generation_input(self, scope, evidence, **hashes):
            records.append((scope, evidence, hashes))
    class Provider:
        async def ainvoke(self, request):
            assert records, 'trace must precede provider invocation'
            self.request = request
            raise RuntimeError('provider failure')
    provider = Provider()
    evidence = [{'id': 'db-chunk-7', 'document_id': 'guide', 'version_id': '4',
                 'rank': 5, 'content': '用软布擦拭。'}]
    state = {'query': '如何清洁？', 'evidence': evidence, 'response_purpose': 'customer_chat',
             'scope': {'subject_type': 'customer'}}
    with pytest.raises(RuntimeError, match='provider failure'):
        asyncio.run(provider_generate_node(state, SimpleNamespace(generation=provider, retriever=Recorder())))
    canonical = json.dumps({k: provider.request[k] for k in ('messages', 'response_schema')},
                           ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    assert records[0][2]['request_sha256'] == hashlib.sha256(canonical.encode()).hexdigest()
    assert records[0][1] == evidence
    assert records[0][1][0]['rank'] == 5


def test_early_clarification_does_not_claim_generation_input():
    class Recorder:
        async def arecord_generation_input(self, *_args, **_kwargs):
            pytest.fail('generation was not constructed')
    asyncio.run(provider_generate_node({'query': '这个？', 'clarification': '请说明商品。',
        'scope': {'subject_type': 'customer'}}, SimpleNamespace(retriever=Recorder())))


def test_persisted_generation_trace_contains_only_identifiers_and_hashes():
    calls = []
    class Connection:
        def __enter__(self): return self
        def __exit__(self, *_): pass
        def execute(self, sql, params): calls.append((sql, params))
    retriever = PostgresRetriever('unused', connect=lambda _: Connection())
    evidence = [{'id': 'db-chunk-7', 'document_id': 'guide', 'version_id': '4',
                 'rank': 5, 'content': 'restricted source body', 'source_ref': '/private/storage'}]
    asyncio.run(retriever.arecord_generation_input(
        {'tenant_db_id': '1', 'subject_type': 'customer', 'request_id': 'e', 'execution_id': 'e'},
        evidence, context_sha256='a'*64, request_sha256='b'*64))
    assert len(calls) == 1 and 'v2_generation_input' in calls[0][0]
    assert 'restricted source body' not in str(calls) and '/private/storage' not in str(calls)
    row = json.loads(calls[0][1][-1])[0]
    assert row['rank'] == 5
    assert row['content_sha256'] == hashlib.sha256(evidence[0]['content'].encode()).hexdigest()
    with pytest.raises(ValueError, match='identity'):
        asyncio.run(retriever.arecord_generation_input({'tenant_db_id': '1', 'subject_type': 'customer',
            'request_id': 'wrong', 'execution_id': 'e'}, evidence, context_sha256='a'*64, request_sha256='b'*64))
