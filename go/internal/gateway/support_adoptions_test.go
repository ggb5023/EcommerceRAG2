package gateway

import (
	"strings"
	"testing"
)

func TestBuildAdoptionPackageIsDeterministicAndInternal(t *testing.T) {
	citations := []suggestionCitation{
		{id: "chunk-1", document: "doc-a", version: "12", shop: "shop-a"},
		{id: "chunk-2", document: "doc-b", version: "7", shop: "shop-a"},
	}
	first := buildAdoptionPackage("shop-a", "  如何退货？\n", "七天内可退。", citations)
	second := buildAdoptionPackage("shop-a", "如何退货？", "七天内可退。", citations)
	if first.DocumentID != second.DocumentID || first.ManifestSHA != second.ManifestSHA {
		t.Fatal("adoption package is not deterministic")
	}
	for _, want := range []string{
		"doc_type: faq", "disclosure_class: internal_only", "source:", "id: staff-suggestions",
		"format: markdown", "pipeline_version: staff-adoption-v1",
	} {
		if !strings.Contains(first.Manifest, want) {
			t.Fatalf("manifest missing %q (%s)", want, first.Manifest)
		}
	}
	body := string(first.File.GetContent())
	for _, want := range []string{"# 如何退货？", "## 顾客问题", "## 建议回答", "1. doc-a / 12 / chunk-1", "2. doc-b / 7 / chunk-2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("markdown missing %q (%s)", want, body)
		}
	}
	other := buildAdoptionPackage("shop-b", "如何退货？", "七天内可退。", citations)
	if other.DocumentID == first.DocumentID {
		t.Fatal("different shops must not share a document id")
	}
}
