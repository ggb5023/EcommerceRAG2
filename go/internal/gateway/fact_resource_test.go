package gateway

import (
	"encoding/json"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestWorkflowDescriptionsDoNotExecuteQuotedFactRequests(t *testing.T) {
	for _, query := range []string{"客户问明天能否送达，应该核对什么？", "如何处理客户问订单状态的问题？"} {
		response := &ragv1.UnderstandResponse{Intent: "factual", RequiredFactTools: []string{"get_logistics"}}
		routeDocumentedWorkflow(response, query)
		if response.Intent != "knowledge" || len(response.RequiredFactTools) != 0 || response.RewrittenQuery != query {
			t.Fatal("workflow description executed a fact request")
		}
	}
	for _, query := range []string{"请查询订单 V2-ORDER-001 的物流", "现在商品 SKU-1 有多少库存？"} {
		response := &ragv1.UnderstandResponse{Intent: "factual", RequiredFactTools: []string{"get_stock"}}
		routeDocumentedWorkflow(response, query)
		if response.Intent != "factual" || len(response.RequiredFactTools) != 1 {
			t.Fatal("direct fact request was rerouted")
		}
	}
}

func TestProductResourceUsesExplicitUnambiguousUserContext(t *testing.T) {
	for _, test := range []struct {
		name, query, user, assistant string
		ok                           bool
	}{
		{"explicit", "现在 SKU-1 库存？", "", "", true},
		{"substring", "SKU-10 库存？", "", "", false},
		{"multiple current", "SKU-1 和 SKU-2 现在库存？", "", "", false},
		{"history", "刚才那件商品还有货吗？", "介绍 SKU-1", "旧库存为99", true},
		{"assistant invention", "刚才那件商品还有货吗？", "介绍耳机", "SKU-1 库存99", false},
		{"ambiguous", "刚才那件商品还有货吗？", "比较 SKU-1 和 SKU-2", "我选 SKU-1", false},
		{"different current", "刚才 SKU-2 库存？", "SKU-1", "", false},
		{"missing reference", "现在还有货吗？", "SKU-1", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			history, _ := json.Marshal([]historyMessage{{Role: "user", Content: test.user}, {Role: "assistant", Content: test.assistant}})
			response := &ragv1.UnderstandResponse{Entities: []*ragv1.EntitySlot{{Name: "sku", Value: "SKU-1", Confidence: .99}}}
			id, ok := routedProductResource(response, test.query, string(history))
			if ok != test.ok || ok && id != "SKU-1" {
				t.Fatalf("resource=(%s,%v) expected=%v", id, ok, test.ok)
			}
		})
	}
}
