"""Customer response recommendations are typed and cannot grant authority."""
import asyncio
from types import SimpleNamespace

import pytest
from app.graph.workflow import provider_generate_node, provider_understand_node, validate_scope
from app.rpc.rag import _valid_scope
from rag.v1 import rag_pb2


def test_customer_scope_is_distinct_and_cannot_expand_to_staff():
    context = rag_pb2.RequestContext(request_id='r', tenant_id='t', user_id='customer:c', role='customer',
        shop_id='s', allowed_shop_ids=['s'], allowed_document_ids=['d'], permission_revision='p',
        enforce_document_scope=True, subject_type='customer', customer_id='c', customer_session_id='cs', conversation_epoch=1)
    assert _valid_scope(context)
    context.all_shops = True
    assert not _valid_scope(context)
    context.all_shops = False
    context.customer_session_id = ''
    assert not _valid_scope(context)
    with pytest.raises(PermissionError):
        validate_scope({'tenant_id': 't', 'user_id': 'c', 'permission_revision': 'p', 'role': 'customer',
                        'allowed_shop_ids': ['s'], 'allowed_document_ids': ['d']})


def test_no_evidence_recommends_handoff_without_a_provider_call():
    result = asyncio.run(provider_generate_node({'query': '未记载的问题', 'response_purpose': 'customer_chat'}, SimpleNamespace()))
    assert result['reply_decision'] == {'behavior': 'HANDOFF', 'reason_code': 'knowledge_missing', 'evidence_ids': []}
    assert not result['can_copy']


def test_missing_context_recommends_clarification_without_publishing():
    result = asyncio.run(provider_generate_node({'query': '这个呢', 'clarification': '请说明商品名称。',
        'response_purpose': 'customer_chat'}, SimpleNamespace()))
    assert result['reply_decision']['behavior'] == 'CLARIFY'
    assert not result['can_copy']


def test_customer_retrieval_purpose_is_external_only():
    from app.retrieval.postgres import PostgresRetriever
    scope = {'tenant_id': 'merchant', 'tenant_db_id': '1',
             'allowed_shop_ids': ['shop'], 'allowed_document_ids': ['doc'],
             'shop_id': 'shop', 'role': 'customer'}
    assert PostgresRetriever._scope_params(scope, 'customer_chat')[-1] == 'customer'
    clause, _ = PostgresRetriever._disclosure_clause('customer_chat', 'owner')
    assert "external_allowed" in clause


def test_guest_control_declares_unavailable_live_capabilities():
    class Control:
        async def ainvoke(self, request):
            self.request = request
            return {'structured': {'rewritten_query': 'Will it arrive tomorrow?', 'intent': 'factual',
                'information_source': 'realtime_business', 'required_fact_tools': [],
                'entities': [], 'clarification': '', 'confidence': .9}}
    control = Control()
    result = asyncio.run(provider_understand_node({'query': 'Will it arrive tomorrow?',
        'scope': {'subject_type': 'customer'}}, SimpleNamespace(control=control)))
    assert result['information_source'] == 'realtime_business'
    assert 'has no live business tools' in control.request['messages'][0]['content']


def test_customer_generation_has_no_staff_capability_or_internal_limit_rendering():
    class Generation:
        async def ainvoke(self, request):
            self.request = request
            return {'structured': {'answer': 'Use a soft sponge.', 'citation_ids': ['chunk'],
                                   'behavior': 'ANSWER', 'reason_code': 'grounded'}}
    generation = Generation()
    evidence = {'id': 'chunk', 'content': 'Use a soft sponge.', 'document_id': 'doc', 'version_id': '1'}
    result = asyncio.run(provider_generate_node({'query': 'How to clean?', 'evidence': [evidence],
        'response_purpose': 'customer_chat'}, SimpleNamespace(generation=generation)))
    assert result['answer'] == 'Use a soft sponge.'
    assert 'evidence_limits' not in generation.request['response_schema']['required']
    assert '不输出document_id' in generation.request['messages'][0]['content']
