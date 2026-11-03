package domain

import (
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func entity(name, value string, confidence float64) *ragv1.EntitySlot {
	return &ragv1.EntitySlot{Name: name, Value: value, Confidence: confidence}
}

func TestCatalogFilterCandidatesUsesOnlyExplicitQueryValues(t *testing.T) {
	response := &ragv1.UnderstandResponse{Entities: []*ragv1.EntitySlot{
		entity("sku", "SKU-CUP-480", .9),
		entity("color", "雾蓝", .9),
		entity("size_label", "M", .9),
		entity("capacity_ml", "480", .9),
		entity("attribute", "material=304不锈钢", .9),
		entity("color", "未出现在问题里", .9),
		entity("sku", "SKU-OTHER", .9),
	}}
	filter := CatalogFilterCandidates(response, "SKU-CUP-480 雾蓝 M 480 304不锈钢 容量")
	if filter == nil {
		t.Fatal("filter is nil")
	}
	if len(filter.SkuCodes) != 1 || filter.SkuCodes[0] != "SKU-CUP-480" {
		t.Fatalf("skus=%v", filter.SkuCodes)
	}
	if len(filter.Colors) != 1 || filter.Colors[0] != "雾蓝" {
		t.Fatalf("colors=%v", filter.Colors)
	}
	if len(filter.SizeLabels) != 1 || filter.SizeLabels[0] != "M" {
		t.Fatalf("sizes=%v", filter.SizeLabels)
	}
	if filter.Attributes["capacity_ml"] != "480" || filter.Attributes["material"] != "304不锈钢" {
		t.Fatalf("attributes=%v", filter.Attributes)
	}
	if _, ok := filter.Attributes["order_id"]; ok {
		t.Fatal("unregistered entity became an attribute")
	}
}

func TestCatalogFilterCandidatesRejectsWeakOrUnbackedValues(t *testing.T) {
	response := &ragv1.UnderstandResponse{Entities: []*ragv1.EntitySlot{
		entity("sku", "SKU-1", .4),
		entity("sku", "SKU-2", 1.1),
		entity("sku", "SKU-3", .9),
		entity("attribute", "material=steel", .9),
	}}
	if filter := CatalogFilterCandidates(response, "SKU-4"); filter != nil {
		t.Fatalf("unexpected filter: %#v", filter)
	}
	if filter := CatalogFilterCandidates(nil, "SKU-3"); filter != nil {
		t.Fatalf("nil response produced %#v", filter)
	}
}
