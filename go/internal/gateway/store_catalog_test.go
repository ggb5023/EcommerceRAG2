package gateway

import (
	"strings"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestCatalogFilterSQLUsesParameterizedRegisteredFields(t *testing.T) {
	normalized, err := normalizeCatalogFilter(&ragv1.CatalogFilter{
		SkuCodes:       []string{"SKU-1"},
		Colors:         []string{"Blue"},
		SizeLabels:     []string{"M"},
		SizeChartCodes: []string{"C1"},
		Attributes:     map[string]string{"capacity_ml": "480"},
	})
	if err != nil {
		t.Fatal(err)
	}
	query, args := catalogFilterSQL(2, "shop-demo", "2026-11-03", normalized)
	for _, want := range []string{
		"lower(cs.sku_code) = ANY($4::text[])",
		"lower(cs.color) = ANY($5::text[])",
		"lower(cs.size_label) = ANY($6::text[])",
		"lower(cs.size_chart_code) = ANY($7::text[])",
		"cs.attributes ->> $8::text = $9",
		"v.effective_to IS NULL OR v.effective_to > $3::date",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query missing %q: %s", want, query)
		}
	}
	if len(args) != 9 || args[4].([]string)[0] != "blue" || args[6].([]string)[0] != "c1" {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestCatalogFilterRejectsUnknownAttributes(t *testing.T) {
	if _, err := normalizeCatalogFilter(&ragv1.CatalogFilter{
		Attributes: map[string]string{"stock": "12"},
	}); err == nil {
		t.Fatal("unknown attribute accepted")
	}
}
