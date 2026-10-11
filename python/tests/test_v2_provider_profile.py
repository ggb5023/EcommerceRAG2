from unittest.mock import patch

import pytest

from app import rag_service
from app.configuration import require_online_budget


class Context:
    def abort(self, code, details):
        raise RuntimeError(f"{code}: {details}")

    def is_active(self):
        return True


def request_context():
    return rag_service.rag_pb2.RequestContext(
        request_id="provider-profile",
        tenant_id="v2-demo-tenant",
        user_id="dev-user",
        shop_id="shop-demo",
        allowed_shop_ids=["shop-demo"],
        allowed_document_ids=["v2-doc-bottle"],
        permission_revision="1",
        role="operator",
        enforce_document_scope=True,
    )


def test_v2_langchain_mock_profile_reaches_grpc_handlers_without_network():
    with patch.object(rag_service, "_PROFILE", "v2_langchain_mock"):
        service = rag_service.RagService(scope_validator=lambda _: None)
        context = request_context()
        understood = service.Understand(
            rag_service.rag_pb2.UnderstandRequest(
                context=context, query="保温杯容量是多少？"
            ),
            Context(),
        )
        assert understood.required_fact_tools == []
        search = list(
            service.Search(
                rag_service.rag_pb2.SearchRequest(
                    context=context,
                    query=understood.rewritten_query,
                    response_purpose="customer_reply",
                ),
                Context(),
            )
        )
        evidence = [item for part in search for item in part.evidence]
        generated = list(
            service.Generate(
                rag_service.rag_pb2.GenerateRequest(
                    context=context,
                    query=understood.rewritten_query,
                    evidence=evidence,
                    response_purpose="customer_reply",
                ),
                Context(),
            )
        )
    assert evidence[0].document_id == "v2-doc-bottle"
    assert any(event.WhichOneof("event") == "done" for event in generated)
    assert any(
        event.WhichOneof("event") == "can_copy" and not event.can_copy
        for event in generated
    )


def test_v2_understand_exposes_fact_tool_requirement_to_go():
    with patch.object(rag_service, "_PROFILE", "v2_langgraph_fixture"):
        response = rag_service.RagService(scope_validator=lambda _: None).Understand(
            rag_service.rag_pb2.UnderstandRequest(
                context=request_context(), query="保温杯价格是多少？"
            ),
            Context(),
        )
    assert list(response.required_fact_tools) == ["get_price"]


def test_v2_profile_marks_only_bailian_as_non_mock():
    with patch.object(rag_service, "_PROFILE", "v2_langgraph_fixture"):
        assert rag_service.RagService._v2_is_mock() is True
    with patch.object(rag_service, "_PROFILE", "v2_langchain_mock"):
        assert rag_service.RagService._v2_is_mock() is True
    with patch.object(rag_service, "_PROFILE", "v2_langchain_bailian"):
        assert rag_service.RagService._v2_is_mock() is False


def test_v2_scope_forwards_signed_execution_identity():
    context = request_context()
    context.conversation_id = "conversation-1"
    context.turn_id = "turn-1"
    context.execution_id = "execution-1"
    forwarded = rag_service.RagService._v2_scope(context)
    assert forwarded["conversation_id"] == "conversation-1"
    assert forwarded["turn_id"] == "turn-1"
    assert forwarded["execution_id"] == "execution-1"


def test_v2_generate_rejects_invalid_fact_before_provider_call():
    with patch.object(rag_service, "_PROFILE", "v2_langchain_mock"):
        service = rag_service.RagService(scope_validator=lambda _: None)
        context = request_context()
        try:
            list(service.Generate(
                rag_service.rag_pb2.GenerateRequest(
                    context=context,
                    query="库存是多少？",
                    facts=[rag_service.rag_pb2.Fact(
                        tool_name="delete_order",
                        value_json="{}",
                        observed_at="2026-10-09T00:00:00Z",
                    )],
                    response_purpose="customer_reply",
                ),
                Context(),
            ))
        except RuntimeError as error:
            assert "PERMISSION_DENIED" in str(error)
        else:
            raise AssertionError("invalid facts must fail closed")


@pytest.mark.parametrize("raw_budget", ["", "0", "-1", "NaN", "Infinity", "not-a-number"])
def test_online_profile_rejects_invalid_budget(monkeypatch, raw_budget):
    monkeypatch.setenv("RAG_PROFILE", "v2_langchain_bailian")
    monkeypatch.setenv("V2_ONLINE_BUDGET_CNY", raw_budget)
    with pytest.raises(ValueError, match="budget"):
        require_online_budget()


def test_online_profile_accepts_positive_finite_budget(monkeypatch):
    monkeypatch.setenv("RAG_PROFILE", "v2_langchain_bailian")
    monkeypatch.setenv("V2_ONLINE_BUDGET_CNY", "12.50")
    require_online_budget()
