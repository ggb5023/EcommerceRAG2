package admin

import "testing"

func TestUsageResponseSeparatesUnattributedRows(t *testing.T) {
	rows := []UsageRow{
		{Slot: "control", Model: "m", InputTokens: 10, OutputTokens: 2, Calls: 1},
		{Unattributed: true, Slot: "search", Model: "tavily", InputTokens: 6, Calls: 2, FailedCalls: 1},
	}
	response := usageResponse(rows)
	if response["input_tokens"] != int64(10) || response["calls"] != int64(1) {
		t.Fatalf("tenant totals include unattributed rows: %+v", response)
	}
	unattributed, ok := response["unattributed"].(usageTotals)
	if !ok || unattributed.InputTokens != 6 || unattributed.Calls != 2 || unattributed.FailedCalls != 1 {
		t.Fatalf("unattributed bucket = %+v", response["unattributed"])
	}
	for _, row := range response["by_slot"].([]usageRow) {
		if row.Slot == "search" {
			t.Fatalf("unattributed slot leaked into by_slot: %+v", row)
		}
	}
	for _, row := range response["by_model"].([]usageRow) {
		if row.Model == "tavily" {
			t.Fatalf("unattributed model leaked into by_model: %+v", row)
		}
	}
}
