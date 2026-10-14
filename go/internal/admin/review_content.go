package admin

import (
	"encoding/json"
	"strings"
)

type parseHint struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

// This is a deliberate projection, never a dump of ingestion meta_json.
type reviewChunk struct {
	ChunkIndex     int            `json:"chunk_index"`
	Content        string         `json:"content"`
	ChunkID        string         `json:"chunk_id"`
	HeadingPath    string         `json:"heading_path"`
	ContentType    string         `json:"content_type"`
	SplitReason    string         `json:"split_reason"`
	SourcePosition map[string]int `json:"source_position"`
	CharStart      *int           `json:"char_start"`
	CharEnd        *int           `json:"char_end"`
	ParseHints     []parseHint    `json:"parse_hints"`
	TableBody      [][]string     `json:"table_body,omitempty"`
	TableCaption   string         `json:"table_caption,omitempty"`
}

func projectReviewContent(value any) ([]reviewChunk, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		reviewChunk
		Position json.RawMessage `json:"source_position"`
		Warning  string          `json:"warning"`
	}
	if err = json.Unmarshal(body, &rows); err != nil {
		return nil, err
	}
	result := make([]reviewChunk, 0, len(rows))
	for _, row := range rows {
		chunk := row.reviewChunk
		var position map[string]json.RawMessage
		_ = json.Unmarshal(row.Position, &position)
		chunk.SourcePosition = nil
		for _, key := range []string{"line_start", "line_end", "record_number", "block_index", "table", "char_start", "char_end"} {
			var number int
			if raw, ok := position[key]; ok && json.Unmarshal(raw, &number) == nil && number >= 0 && (strings.HasPrefix(key, "char_") || number > 0) {
				if chunk.SourcePosition == nil {
					chunk.SourcePosition = map[string]int{}
				}
				chunk.SourcePosition[key] = number
			}
		}
		chunk.ParseHints = []parseHint{}
		switch {
		case strings.HasPrefix(row.Warning, "code_language:"):
			chunk.ParseHints = append(chunk.ParseHints, parseHint{"info", "code_language"})
		case row.Warning == "repeated_table_header", row.Warning == "image_reference_only":
			chunk.ParseHints = append(chunk.ParseHints, parseHint{"info", row.Warning})
		case row.Warning == "unterminated_code_fence":
			chunk.ParseHints = append(chunk.ParseHints, parseHint{"structure", row.Warning})
		case row.Warning != "":
			// Unknown diagnostics remain visible without exposing arbitrary free text.
			chunk.ParseHints = append(chunk.ParseHints, parseHint{"structure", "parser_warning"})
		}
		result = append(result, chunk)
	}
	return result, nil
}
