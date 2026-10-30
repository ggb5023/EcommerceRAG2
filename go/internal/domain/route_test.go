package domain

import (
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestDecideClarificationForClarificationIntent(t *testing.T) {
	routed := &ragv1.UnderstandResponse{Intent: "clarification", Clarification: "请提供订单号。", Confidence: 0.9}
	decision := DecideClarification(routed, false)
	if !decision.Needed || decision.Text != "请提供订单号。" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecideClarificationUsesDefaultText(t *testing.T) {
	routed := &ragv1.UnderstandResponse{Intent: "clarification", Confidence: 0.9}
	decision := DecideClarification(routed, false)
	if !decision.Needed || decision.Text != "请补充更多问题细节。" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestDecideClarificationShortCircuitsOnSnapshotFallback(t *testing.T) {
	routed := &ragv1.UnderstandResponse{Intent: "clarification", Clarification: "请提供订单号。", Confidence: 0.9}
	if decision := DecideClarification(routed, true); decision.Needed {
		t.Fatalf("snapshot fallback must not ask again: %+v", decision)
	}
}

func TestDecideClarificationIgnoresConfidentKnowledge(t *testing.T) {
	routed := &ragv1.UnderstandResponse{Intent: "knowledge", Clarification: "补充说明", Confidence: 0.9}
	if decision := DecideClarification(routed, false); decision.Needed {
		t.Fatalf("a confident knowledge answer must not clarify: %+v", decision)
	}
}
