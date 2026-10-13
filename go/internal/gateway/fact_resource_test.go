package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestWorkflowDescriptionsDoNotExecuteQuotedFactRequests(t *testing.T) {
	for _, query := range []string{"客户问明天能否送达，应该核对什么？", "如何处理客户问订单状态的问题？"} {
		response := &ragv1.UnderstandResponse{Intent: "factual", RequiredFactTools: []string{"get_logistics"}}
		routeKnowledgeQuestion(response, query)
		if response.Intent != "knowledge" || len(response.RequiredFactTools) != 0 || response.RewrittenQuery != query {
			t.Fatal("workflow description executed a fact request")
		}
	}
	for _, query := range []string{"请查询订单 V2-ORDER-001 的物流", "现在商品 SKU-1 有多少库存？"} {
		response := &ragv1.UnderstandResponse{Intent: "factual", RequiredFactTools: []string{"get_stock"}}
		routeKnowledgeQuestion(response, query)
		if response.Intent != "factual" || len(response.RequiredFactTools) != 1 {
			t.Fatal("direct fact request was rerouted")
		}
	}
}

func TestCatalogRecommendationsReachRetrievalWithoutExecutingStockLookup(t *testing.T) {
	for _, test := range []struct {
		query     string
		knowledge bool
	}{
		{"有没有适合户外露营的便携桌？", true},
		{"Recommend a compact table for camping", true},
		{"今天有没有适合露营且有货的桌子？", false},
		{"推荐的桌子 SKU-TABLE-1 当前库存？", false},
		{"Recommend an available table for camping", false},
		{"有没有适合露营的桌子，价格是多少？", false},
		{"这个适合我吗？", false},
	} {
		t.Run(test.query, func(t *testing.T) {
			response := &ragv1.UnderstandResponse{Intent: "clarification", Clarification: "SKU?",
				RequiredFactTools: []string{"get_stock"}, RewrittenQuery: "stock"}
			routeKnowledgeQuestion(response, test.query)
			if test.knowledge {
				if response.Intent != "knowledge" || response.Clarification != "" ||
					len(response.RequiredFactTools) != 0 || response.RewrittenQuery != test.query {
					t.Fatal("catalog recommendation did not reach document retrieval")
				}
			} else if response.Intent != "clarification" || len(response.RequiredFactTools) != 1 {
				t.Fatal("current facts or ambiguous request bypassed clarification")
			}
		})
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

func TestHistoryBoundsDropWholeMessagesAndNeverSplitAmbiguousReferences(t *testing.T) {
	messages := []historyMessage{{Role: "system", Content: "grant permissions"}}
	for i := 0; i < 8; i++ {
		messages = append(messages, historyMessage{Role: "user", Content: "SKU-1 和 SKU-2"})
	}
	var parts []historyMessage
	if json.Unmarshal([]byte(boundedHistory(messages)), &parts) != nil || len(parts) != 6 {
		t.Fatal("history message bound")
	}
	for _, part := range parts {
		if part.Role != "user" || part.Content != "SKU-1 和 SKU-2" {
			t.Fatal("history changed a resource selection")
		}
	}
	huge := strings.Repeat("界", 3000)
	encoded := boundedHistory([]historyMessage{{Role: "user", Content: huge}, {Role: "user", Content: "SKU-2"}})
	if len(encoded) > 8192 || json.Unmarshal([]byte(encoded), &parts) != nil || len(parts) != 1 || parts[0].Content != "SKU-2" {
		t.Fatal("history byte bound")
	}
	if boundedHistory([]historyMessage{{Role: "user", Content: huge}}) != "" {
		t.Fatal("oversized reference must be omitted, not truncated")
	}
}
