package domain

import (
	"math"
	"slices"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestRoutedOrderResourceRequiresOneConfidentValidEntity(t *testing.T) {
	for _, test := range []struct {
		name     string
		entities []*ragv1.EntitySlot
		query    string
		want     string
		ok       bool
	}{
		{name: "valid", entities: []*ragv1.EntitySlot{{Name: "order_id", Value: "V2-ORDER-001", Confidence: .99}}, want: "V2-ORDER-001", ok: true},
		{name: "invented", query: "订单号 OTHER-ORDER 状态", entities: []*ragv1.EntitySlot{{Name: "order_id", Value: "V2-ORDER-001", Confidence: .99}}},
		{name: "substring", query: "订单号 V2-ORDER-0010 状态", entities: []*ragv1.EntitySlot{{Name: "order_id", Value: "V2-ORDER-001", Confidence: .99}}},
		{name: "nan", entities: []*ragv1.EntitySlot{{Name: "order_id", Value: "V2-ORDER-001", Confidence: math.NaN()}}},
		{name: "infinity", entities: []*ragv1.EntitySlot{{Name: "order_id", Value: "V2-ORDER-001", Confidence: math.Inf(1)}}},
		{name: "missing", ok: false},
		{name: "low_confidence", entities: []*ragv1.EntitySlot{{Name: "order_id", Value: "V2-ORDER-001", Confidence: .49}}, ok: false},
		{name: "ambiguous", entities: []*ragv1.EntitySlot{{Name: "order_id", Value: "V2-ORDER-001", Confidence: .99}, {Name: "order_id", Value: "V2-ORDER-002", Confidence: .99}}, ok: false},
		{name: "malformed", entities: []*ragv1.EntitySlot{{Name: "order_id", Value: "../secret", Confidence: .99}}, ok: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := test.query
			if query == "" {
				query = "订单号 V2-ORDER-001 的状态"
			}
			got, ok := RoutedOrderResource(&ragv1.UnderstandResponse{Entities: test.entities}, query)
			if got != test.want || ok != test.ok {
				t.Fatalf("RoutedOrderResource()=(%q,%v), want (%q,%v)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestExplicitOrderReferencesRequireAnOrderCue(t *testing.T) {
	for _, test := range []struct {
		name  string
		query string
		want  []string
	}{
		{"chinese cue", "订单号 V2-ORDER-001 到哪了", []string{"V2-ORDER-001"}},
		{"chinese cue with colon", "订单编号：ORD-2609-0006", []string{"ORD-2609-0006"}},
		{"chinese cue with verb", "订单号是 V2-ORDER-001", []string{"V2-ORDER-001"}},
		{"chinese cue without space", "订单号V2-ORDER-001", []string{"V2-ORDER-001"}},
		{"english cue", "Order ID: ORDER-9 status", []string{"ORDER-9"}},
		{"two cues", "订单号 V2-ORDER-001 和订单编号 ORD-2609-0006", []string{"V2-ORDER-001", "ORD-2609-0006"}},
		{"sku only has no order cue", "CLB-DRK-002 有库存吗", nil},
		{"bare identifier has no order cue", "V2-ORDER-001 到哪了", nil},
		{"cue without a literal", "订单号是多少", nil},
		{"sku after a bound order is not another reference", "订单号 ORD-2609-0005 里的 CLB-DRK-002 有库存吗", []string{"ORD-2609-0005"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ExplicitOrderReferences(test.query)
			if !slices.Equal(got, test.want) {
				t.Fatalf("ExplicitOrderReferences(%q) = %v, want %v", test.query, got, test.want)
			}
		})
	}
}

func TestIsOrderIdentifierAndResourceTokens(t *testing.T) {
	if !IsOrderIdentifier("V2-ORDER-001") || IsOrderIdentifier("../secret") || IsOrderIdentifier("") {
		t.Fatal("identifier shape check is wrong")
	}
	tokens := ResourceTokens("订单号 V2-ORDER-0010 状态")
	if !slices.Contains(tokens, "V2-ORDER-0010") || !slices.Contains(tokens, "V2-ORDER-0010") {
		t.Fatalf("tokens = %v", tokens)
	}
	if !slices.Contains(ResourceTokens("单号 V2-ORDER-001"), "V2-ORDER-001") {
		t.Fatal("an exact identifier must tokenize to itself")
	}
}
