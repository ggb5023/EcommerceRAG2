package admin

import "testing"

func TestFailedTurnIsNotSolved(t *testing.T) {
	summary := summarizeResolution([]resolutionTurn{
		outcomeTurn(1, 0, resolvedState("FAILED")),
	}, outcomesBase)
	if summary.Failed != 1 || summary.Solved != 0 || summary.ScoredTotal != 1 {
		t.Fatalf("summary = %+v, want failed=1 solved=0 scored_total=1", summary)
	}
	if summary.AutoRate == nil || *summary.AutoRate != 0 {
		t.Fatalf("auto_rate = %v, want 0", summary.AutoRate)
	}
}
