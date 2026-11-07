"""Customer response recommendations are typed and cannot grant authority."""
import asyncio
from types import SimpleNamespace

import pytest

from app.graph.service import LangGraphRagService
from app.graph.workflow import (
    provider_generate_node,
    provider_understand_node,
    validate_scope,
)
from app.retrieval.postgres import PostgresRetriever
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


def test_customer_scope_lane_binding_is_required():
    base = {'tenant_id': 't', 'user_id': 'c', 'permission_revision': 'p', 'role': 'customer',
            'allowed_shop_ids': ['s'], 'allowed_document_ids': ['d'], 'subject_type': 'customer',
            'customer_id': 'c', 'customer_session_id': 'cs', 'conversation_epoch': 1}
    validate_scope(dict(base))
    validate_scope(dict(base, service_type='pre_sales'))
    validate_scope(dict(base, service_type='after_sales', customer_order_id='ORD-2609-0005'))
    with pytest.raises(PermissionError, match='order binding'):
        validate_scope(dict(base, service_type='after_sales'))
    with pytest.raises(PermissionError, match='order binding'):
        validate_scope(dict(base, service_type='pre_sales', customer_order_id='ORD-2609-0005'))
    with pytest.raises(PermissionError, match='service lane'):
        validate_scope(dict(base, service_type='after_sale'))
    empty = dict(base, allowed_document_ids=[], service_type='after_sales',
                 customer_order_id='ORD-2609-0005')
    validate_scope(empty)
    with pytest.raises(PermissionError, match='document grants'):
        validate_scope(dict(empty, service_type='pre_sales', customer_order_id=''))


def test_after_sales_control_declares_the_bound_read_only_order():
    class Control:
        async def ainvoke(self, request):
            self.request = request
            return {'structured': {'rewritten_query': '订单到哪了？', 'intent': 'factual',
                'information_source': 'realtime_business', 'required_fact_tools': ['get_logistics'],
                'entities': [], 'clarification': '', 'confidence': .9}}
    control = Control()
    asyncio.run(provider_understand_node({'query': '订单到哪了？',
        'scope': {'subject_type': 'customer', 'service_type': 'after_sales',
                  'customer_order_id': 'ORD-2609-0005'}}, SimpleNamespace(control=control)))
    system = control.request['messages'][0]['content']
    assert 'bound to exactly one order' in system
    assert 'cannot refund' in system
    assert 'ORD-2609-0005' in system
    assert 'entity named order_id' in system


def test_after_sales_generation_allows_fact_snapshots_but_forbids_writes():
    class Generation:
        async def ainvoke(self, request):
            self.request = request
            return {'structured': {'answer': '订单已发货，物流显示运输中。', 'citation_ids': [],
                                   'behavior': 'ANSWER', 'reason_code': 'grounded'}}
    generation = Generation()
    facts = [{'tool_name': 'get_logistics', 'resource_id': 'ORD-2609-0005',
              'observed_at': '2026-09-29T12:00:00Z', 'is_mock': True,
              'value_json': '{"order_id": "ORD-2609-0005", "status": "in_transit"}'}]
    result = asyncio.run(provider_generate_node({'query': '订单到哪了？', 'evidence': [], 'facts': facts,
        'response_purpose': 'customer_chat',
        'scope': {'subject_type': 'customer', 'service_type': 'after_sales'}}, SimpleNamespace(generation=generation)))
    system = generation.request['messages'][0]['content']
    assert '售后只读客服场景' in system
    assert '已退款' in system
    assert '100分=1元' in system
    assert 'is_mock' in system
    assert result['reply_decision']['behavior'] == 'ANSWER'


def test_after_sales_scope_may_have_an_empty_document_grant():
    scope = {'tenant_id': 'v2-demo-tenant', 'tenant_db_id': '1', 'user_id': 'customer:c',
             'role': 'customer', 'subject_type': 'customer', 'customer_id': 'c',
             'customer_session_id': 'cs', 'conversation_epoch': 1,
             'allowed_shop_ids': ['shop-demo'], 'allowed_document_ids': [],
             'shop_id': 'shop-demo', 'service_type': 'after_sales',
             'customer_order_id': 'ORD-2609-0005'}
    params = PostgresRetriever._scope_params(scope, 'customer_chat')
    assert params[3] == []
    with pytest.raises(PermissionError):
        PostgresRetriever._scope_params(dict(scope, service_type='pre_sales', customer_order_id=''), 'customer_chat')
    with pytest.raises(PermissionError):
        PostgresRetriever._scope_params(dict(scope, role='operator', subject_type=''), 'internal')


def test_fact_scope_binding_rejects_a_different_order():
    service = LangGraphRagService()
    scope = {'role': 'customer', 'subject_type': 'customer', 'service_type': 'after_sales',
             'customer_order_id': 'ORD-2609-0005'}
    fact = {'tool_name': 'get_logistics', 'resource_id': 'ORD-2609-0006',
            'observed_at': '2026-09-29T12:00:00Z', 'is_mock': True,
            'value_json': '{"order_id": "ORD-2609-0006", "status": "in_transit"}'}
    with pytest.raises(PermissionError, match='bound customer order'):
        service._validate_facts([fact], scope)
    fact['resource_id'] = 'ORD-2609-0005'
    fact['value_json'] = '{"order_id": "ORD-2609-0005", "status": "in_transit"}'
    assert service._validate_facts([fact], scope)


def test_customer_fact_scope_rejects_compare_products_and_pre_sales():
    service = LangGraphRagService()
    after = {'role': 'customer', 'subject_type': 'customer', 'service_type': 'after_sales',
             'customer_order_id': 'ORD-2609-0005'}
    comparison = {'tool_name': 'compare_products', 'resource_id': '',
                  'observed_at': '2026-09-29T12:00:00Z', 'is_mock': True,
                  'value_json': '{}'}
    with pytest.raises(PermissionError, match='after-sales customer fact tool'):
        service._validate_facts([comparison], after)
    pre = {'role': 'customer', 'subject_type': 'customer', 'service_type': 'pre_sales',
           'customer_order_id': ''}
    with pytest.raises(PermissionError, match='pre-sales customer scope'):
        service._validate_facts([dict(comparison, tool_name='get_order_status',
                                      resource_id='ORD-2609-0005',
                                      value_json='{"order_id": "ORD-2609-0005"}')], pre)


def test_after_sales_bound_order_skips_the_missing_reference_heuristic():
    class Control:
        async def ainvoke(self, request):
            self.request = request
            return {'structured': {'rewritten_query': '订单到哪了', 'intent': 'factual',
                'information_source': 'realtime_business', 'required_fact_tools': ['get_logistics'],
                'entities': [], 'clarification': '', 'confidence': .9}}
    control = Control()
    asyncio.run(provider_understand_node({'query': '刚才那个订单到哪了？',
        'scope': {'subject_type': 'customer', 'service_type': 'after_sales',
                  'customer_order_id': 'ORD-2609-0005'}}, SimpleNamespace(control=control)))
    assert 'factual' in control.request['response_schema']['properties']['intent']['enum']
