from app.graph.service import LangGraphRagService


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


def test_graph_runs_nodes_and_preserves_customer_scope():
    result = LangGraphRagService().run(
        query="保温杯容量是多少？",
        scope=scope("v2-doc-bottle", "v2-doc-internal"),
        response_purpose="customer_reply",
    )
    assert result["is_mock"] is True
    assert result["can_copy"] is False
    assert result["evidence"][0]["document_id"] == "v2-doc-bottle"
    assert all(item["document_id"] != "v2-doc-internal" for item in result["evidence"])
    assert result["trace"]["nodes"] == ["understand", "retrieve", "fuse_rrf", "rerank", "context_gate", "generate"]


def test_graph_rejects_empty_grants():
    bad = scope()
    try:
        LangGraphRagService().run(query="配送多久？", scope=bad)
    except PermissionError as error:
        assert "finite" in str(error)
    else:
        raise AssertionError("empty document grants must fail closed")


def test_graph_clarifies_vague_question_without_copy_permission():
    result = LangGraphRagService().run(query="这个呢？", scope=scope("v2-doc-bottle"), response_purpose="customer_reply")
    assert result["answer"] == "请补充商品或具体问题。"
    assert result["can_copy"] is False


def test_graph_routes_factual_requests_to_go_owned_tools():
    result = LangGraphRagService().run(
        query="保温杯价格是多少？",
        scope=scope("v2-doc-bottle"),
        response_purpose="internal",
    )
    assert result["required_fact_tools"] == ["get_price"]


def test_go_seed_evidence_is_scope_checked_before_graph_execution():
    service = LangGraphRagService()
    evidence = [{
        "id": "outside", "content": "不应进入图", "source_type": "fixture",
        "source_ref": "fixture://outside", "document_id": "other-doc",
        "version_id": "v1", "tenant_id": "v2-demo-tenant", "shop_id": "shop-demo",
        "disclosure_class": "external_allowed", "customer_eligible": True,
    }]
    try:
        service.run_with_evidence(query="问题", scope=scope("v2-doc-bottle"), evidence=evidence)
    except PermissionError as error:
        assert "document" in str(error)
    else:
        raise AssertionError("out-of-scope evidence must fail closed")


def test_fact_contract_rejects_unknown_tools_and_accepts_authorized_shape():
    service = LangGraphRagService()
    facts = service._validate_facts([{
        "tool_name": "get_stock",
        "value_json": '{"sku":"SKU-1","available":4}',
        "resource_id": "SKU-1",
        "observed_at": "2026-10-09T00:00:00Z",
        "is_mock": True,
    }])
    assert facts[0]["tool_name"] == "get_stock"
    bound = service._validate_facts([{
        "tool_name": "get_order_status", "resource_id": "V2-ORDER-001",
        "value_json": '{"order_id":"V2-ORDER-001","status":"in_transit"}',
        "observed_at": "2026-10-12T00:00:00Z", "is_mock": True,
    }])
    assert bound[0]["resource_id"] == "V2-ORDER-001"
    for invalid in [
        {"tool_name": "get_stock", "resource_id": "", "value_json": '{"sku":"SKU-1"}', "observed_at": "2026-10-12T00:00:00Z"},
        {"tool_name": "get_price", "resource_id": "SKU-1", "value_json": '{"sku":"OTHER-SKU"}', "observed_at": "2026-10-12T00:00:00Z"},
        {"tool_name": "get_order_status", "resource_id": "", "value_json": '{"order_id":"V2-ORDER-001"}', "observed_at": "2026-10-12T00:00:00Z"},
        {"tool_name": "get_logistics", "resource_id": "V2-ORDER-001", "value_json": '{"order_id":"OTHER-ORDER"}', "observed_at": "2026-10-12T00:00:00Z"},
    ]:
        try:
            service._validate_facts([invalid])
        except PermissionError:
            pass
        else:
            raise AssertionError("unbound or mismatched order facts must fail closed")
    try:
        service._validate_facts([{
            "tool_name": "delete_order",
            "value_json": "{}",
            "observed_at": "2026-10-09T00:00:00Z",
        }])
    except PermissionError as error:
        assert "fact" in str(error)
    else:
        raise AssertionError("unknown fact tools must fail closed")


def test_context_relevance_narrows_search_but_preserves_signed_generate_snapshot():
    from app.graph.workflow import context_gate_node
    rows = [dict(id=str(i), rerank_score=s, disclosure_class='external_allowed', customer_eligible=True)
            for i, s in enumerate([.92, .75, .64, .3])]
    state = dict(response_purpose='customer_reply', evidence=rows)
    assert [e['id'] for e in context_gate_node(state)['evidence']] == ['0', '1']
    assert context_gate_node(dict(state, seed_evidence=rows))['evidence'] == rows
    low = [dict(r, rerank_score=r['rerank_score'] / 10) for r in rows]
    assert [e['id'] for e in context_gate_node(dict(state, evidence=low))['evidence']] == ['0', '1']
    private = [dict(rows[0], disclosure_class='internal_only'), rows[1]]
    assert context_gate_node(dict(state, evidence=private))['evidence'] == [rows[1]]
