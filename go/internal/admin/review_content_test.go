package admin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReviewProjectionPreservesStructureWithoutInternalMetadata(t *testing.T) {
	input := []map[string]any{{"chunk_index": 3, "content": "A | B", "chunk_id": "db-chunk-42",
		"heading_path": "Policy / Returns", "content_type": "table", "split_reason": "table_boundary",
		"source_position": map[string]any{"block_index": 4, "table": 1, "char_start": 0, "char_end": 5, "page_no": 12, "storage_key": "private-object"},
		"warning":         "repeated_table_header", "table_body": [][]string{{"A", "B"}}, "storage_key": "private-object"}}
	rows, err := projectReviewContent(input)
	if err != nil || len(rows) != 1 || rows[0].SourcePosition["block_index"] != 4 || len(rows[0].TableBody) != 1 || rows[0].ParseHints[0].Kind != "info" {
		t.Fatalf("invalid review projection: %v", err)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), "private-object") || strings.Contains(string(encoded), "page_no") || strings.Contains(string(encoded), "warning\"") {
		t.Fatal("review leaks unapproved internal metadata")
	}
}

func TestReviewOldPositionAndHintClassification(t *testing.T) {
	rows, err := projectReviewContent([]map[string]any{{"chunk_index": 0, "content": "old", "char_start": 0, "char_end": 3},
		{"warning": "code_language:python"}, {"warning": "unterminated_code_fence"}, {"warning": "private-object"},
		{"source_position": map[string]any{"line_start": -1, "line_end": "secret", "block_index": 0}}})
	if err != nil || rows[0].SourcePosition != nil || *rows[0].CharEnd != 3 || len(rows[0].ParseHints) != 0 || rows[4].SourcePosition != nil {
		t.Fatal("old or invalid locations must remain unavailable")
	}
	if rows[1].ParseHints[0].Kind != "info" || rows[2].ParseHints[0].Kind != "structure" || rows[3].ParseHints[0].Code != "parser_warning" {
		t.Fatal("parser hints misclassified")
	}
}
