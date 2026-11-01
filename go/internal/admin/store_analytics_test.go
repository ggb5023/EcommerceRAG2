package admin

import (
	"math"
	"testing"
	"time"
)

var outcomesBase = time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)

func resolvedState(value string) *string { return &value }

func outcomeTurn(conversation int64, minute int, state *string) resolutionTurn {
	return resolutionTurn{ConversationID: conversation, CreatedAt: outcomesBase.Add(time.Duration(minute) * time.Minute), State: state}
}

func almostEqual(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

func TestResolutionSummaryEmptyWindow(t *testing.T) {
	summary := summarizeResolution(nil)
	if summary.Total != 0 || summary.Solved != 0 || summary.Clarified != 0 || summary.Failed != 0 || summary.Unknown != 0 {
		t.Fatalf("empty window = %+v, want all zero", summary)
	}
	if summary.AutoRate != nil || summary.AssistedRate != nil {
		t.Fatalf("empty window rates = %v/%v, want null/null", summary.AutoRate, summary.AssistedRate)
	}
}

func TestResolutionSummaryCounts(t *testing.T) {
	summary := summarizeResolution([]resolutionTurn{
		outcomeTurn(1, 0, resolvedState("DONE")),
		outcomeTurn(2, 0, resolvedState("ASKING")),
		outcomeTurn(2, 10, resolvedState("DONE")),
		outcomeTurn(3, 0, resolvedState("FAILED")),
		// A DONE that the same conversation replaced within five minutes is not
		// a solve; only the replacement is.
		outcomeTurn(4, 0, resolvedState("DONE")),
		outcomeTurn(4, 3, resolvedState("DONE")),
		outcomeTurn(5, 0, nil),
		// Beyond the five-minute window the earlier DONE still counts.
		outcomeTurn(6, 0, resolvedState("DONE")),
		outcomeTurn(6, 6, resolvedState("FAILED")),
	})
	if summary.Total != 9 || summary.Solved != 4 || summary.Clarified != 1 || summary.Failed != 2 || summary.Unknown != 1 {
		t.Fatalf("summary = %+v, want total=9 solved=4 clarified=1 failed=2 unknown=1", summary)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 4.0/7.0) {
		t.Fatalf("auto_rate = %v, want 4/7", summary.AutoRate)
	}
	if summary.AssistedRate == nil || !almostEqual(*summary.AssistedRate, 5.0/7.0) {
		t.Fatalf("assisted_rate = %v, want 5/7", summary.AssistedRate)
	}
}

func TestResolutionSummaryUnknownExcludedFromDenominator(t *testing.T) {
	summary := summarizeResolution([]resolutionTurn{
		outcomeTurn(1, 0, resolvedState("DONE")),
		outcomeTurn(2, 0, nil),
		outcomeTurn(3, 0, nil),
		outcomeTurn(4, 0, nil),
	})
	if summary.Unknown != 3 || summary.Solved != 1 {
		t.Fatalf("summary = %+v, want unknown=3 solved=1", summary)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 1) {
		t.Fatalf("auto_rate = %v, want 1 (unknown turns excluded)", summary.AutoRate)
	}
	if summary.AssistedRate == nil || !almostEqual(*summary.AssistedRate, 1) {
		t.Fatalf("assisted_rate = %v, want 1 (unknown turns excluded)", summary.AssistedRate)
	}
}
