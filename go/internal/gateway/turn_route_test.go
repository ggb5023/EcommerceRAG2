package gateway

import (
	"strings"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

// resolveFactIntent returns a new response; the input is read only. These tests
// pin both the returned decision and the absence of input mutation.
func TestResolveFactIntentOrderBranch(t *testing.T) {
	routed := &ragv1.UnderstandResponse{RequiredFactTools: []string{"get_order_status"}, Intent: "factual",
		Entities: []*ragv1.EntitySlot{{Name: "order_id", Value: "20261028001", Confidence: 0.9}}}
	resolved, resource, snapshotFallback := resolveFactIntent(routed, "订单 20261028001 到哪了", "")
	if snapshotFallback {
		t.Fatal("a valid order id must not fall back")
	}
	if resource != "20261028001" {
		t.Fatalf("resource = %q, want the order id", resource)
	}
	if len(resolved.RequiredFactTools) != 1 || resolved.RequiredFactTools[0] != "get_order_status" {
		t.Fatalf("tools = %v", resolved.RequiredFactTools)
	}
	if len(routed.RequiredFactTools) != 1 || routed.RequiredFactTools[0] != "get_order_status" {
		t.Fatalf("input was mutated to %v", routed.RequiredFactTools)
	}
}

func TestResolveFactIntentProductBranch(t *testing.T) {
	routed := &ragv1.UnderstandResponse{RequiredFactTools: []string{"get_stock"}, Intent: "factual",
		Entities: []*ragv1.EntitySlot{{Name: "sku", Value: "SKU-CUP-480", Confidence: 0.9}}}
	_, resource, snapshotFallback := resolveFactIntent(routed, "SKU-CUP-480 有货吗", "")
	if snapshotFallback {
		t.Fatal("a valid sku must not fall back")
	}
	if resource != "SKU-CUP-480" {
		t.Fatalf("resource = %q, want the sku", resource)
	}
}

func TestResolveFactIntentUnknownKeepsTools(t *testing.T) {
	routed := &ragv1.UnderstandResponse{RequiredFactTools: []string{"compare_products"}, Intent: "knowledge"}
	resolved, resource, snapshotFallback := resolveFactIntent(routed, "帮我看看", "")
	if resource != "" || snapshotFallback {
		t.Fatalf("unknown tools must not address a resource: resource=%q fallback=%v", resource, snapshotFallback)
	}
	if len(resolved.RequiredFactTools) != 1 || resolved.RequiredFactTools[0] != "compare_products" {
		t.Fatalf("returned tools = %v", resolved.RequiredFactTools)
	}
	if len(routed.RequiredFactTools) != 1 {
		t.Fatalf("input was mutated to %v", routed.RequiredFactTools)
	}
}

func TestResolveFactIntentInvalidOrderReturnsNewClarification(t *testing.T) {
	routed := &ragv1.UnderstandResponse{RequiredFactTools: []string{"get_logistics"}, Intent: "factual", Confidence: 0.9}
	resolved, resource, snapshotFallback := resolveFactIntent(routed, "物流到哪了", "")
	if resource != "" || !snapshotFallback {
		t.Fatalf("missing order id must fall back: resource=%q fallback=%v", resource, snapshotFallback)
	}
	if resolved.Intent != "clarification" || resolved.RequiredFactTools != nil || resolved.Confidence != 0.2 {
		t.Fatalf("returned response: intent=%q tools=%v confidence=%v", resolved.Intent, resolved.RequiredFactTools, resolved.Confidence)
	}
	if !strings.Contains(resolved.Clarification, "订单号") {
		t.Fatalf("clarification = %q", resolved.Clarification)
	}
	if routed.Intent != "factual" || len(routed.RequiredFactTools) != 1 || routed.Confidence != 0.9 || routed.Clarification != "" {
		t.Fatalf("input mutated: %+v", routed)
	}
}

func TestFinalAnswerAppliesSnapshotQualifier(t *testing.T) {
	plain := finalAnswer(&generationResult{answer: "标准配送通常需要 2-5 个工作日。"}, false)
	if strings.Contains(plain, "快照") {
		t.Fatalf("plain answer gained a qualifier: %q", plain)
	}
	qualified := finalAnswer(&generationResult{answer: "标准配送通常需要 2-5 个工作日。"}, true)
	if !strings.Contains(qualified, "快照") || !strings.Contains(qualified, "标准配送") {
		t.Fatalf("snapshot answer not qualified: %q", qualified)
	}
	// Pinned current behaviour: an empty answer still receives the standalone
	// qualifier, so the customer never sees a blank bubble on the fallback path.
	if got := finalAnswer(&generationResult{answer: ""}, true); got != "来自快照，不能保证当前状态。" {
		t.Fatalf("empty answer qualifier = %q", got)
	}
}
