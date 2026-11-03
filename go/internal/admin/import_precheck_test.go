package admin

import (
	"encoding/json"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestApplyPackageValidationMapsRowErrors(t *testing.T) {
	detail := importStagedDetail{}
	document := &ragv1.ValidatedPackageDocument{
		TableSchema: "product_table_v1",
		RowCount:    7,
		ErrorCount:  2,
		Truncated:   true,
		RowErrors: []*ragv1.ValidatedPackageRowError{{
			Row: 2, Column: "sku_code", Code: "duplicate_sku", Severity: "block",
			Value: "SKU-1", Message: "重复", SuggestedAction: "修正",
		}},
	}
	applyPackageValidation(&detail, document)
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["table_schema"] != "product_table_v1" || decoded["row_count"].(float64) != 7 ||
		decoded["error_count"].(float64) != 2 || decoded["truncated"] != true {
		t.Fatalf("unexpected detail: %#v", decoded)
	}
	rows, ok := decoded["row_errors"].([]any)
	if !ok || len(rows) != 1 || rows[0].(map[string]any)["column"] != "sku_code" {
		t.Fatalf("row errors not mapped: %#v", decoded["row_errors"])
	}
}

func TestBlockingPrecheckRejectsDocumentAndRowBlocks(t *testing.T) {
	docBlock, _ := json.Marshal([]ImportIssue{{IssueCode: "missing_source", Severity: "block"}})
	warnOnly, _ := json.Marshal([]ImportIssue{{IssueCode: "duplicate_import", Severity: "warn"}})
	rowWarn, _ := json.Marshal(importStagedDetail{
		RowErrors: []importRowError{{Row: 2, Code: "unknown_column", Severity: "warn"}},
	})
	rowBlock, _ := json.Marshal(importStagedDetail{
		RowErrors: []importRowError{{Row: 2, Code: "duplicate_sku", Severity: "block"}},
	})
	if !blockingPrecheck(docBlock, nil) {
		t.Fatal("document block was not detected")
	}
	if !blockingPrecheck(warnOnly, rowBlock) {
		t.Fatal("row block was not detected")
	}
	if blockingPrecheck(warnOnly, rowWarn) {
		t.Fatal("warnings must not block confirmation")
	}
}

func TestRowErrorCounts(t *testing.T) {
	blocks, warnings := rowErrorCounts([]*ragv1.ValidatedPackageRowError{
		{Severity: "block"}, {Severity: "warn"}, {Severity: "warn"},
	})
	if blocks != 1 || warnings != 2 {
		t.Fatalf("blocks=%d warnings=%d", blocks, warnings)
	}
}
