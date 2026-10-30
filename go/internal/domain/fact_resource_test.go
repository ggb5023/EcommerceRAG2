package domain

import (
	"math"
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
