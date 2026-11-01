package admin

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var outcomesBase = time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
var nextResolutionTurnID int64

func resolvedState(value string) *string { return &value }

func outcomeTurn(conversation int64, minute int, state *string) resolutionTurn {
	nextResolutionTurnID++
	return resolutionTurn{TurnID: nextResolutionTurnID, ConversationID: conversation,
		CreatedAt: outcomesBase.Add(time.Duration(minute) * time.Minute), State: state}
}

func at(minute int) *time.Time {
	value := outcomesBase.Add(time.Duration(minute) * time.Minute)
	return &value
}

func almostEqual(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

func TestResolutionSummaryEmptyWindow(t *testing.T) {
	summary := summarizeResolution(nil, outcomesBase)
	if summary.TurnsTotal != 0 || summary.ScoredTotal != 0 || summary.Solved != 0 || summary.Clarified != 0 ||
		summary.Abandoned != 0 || summary.Failed != 0 || summary.Cancelled != 0 || summary.Pending != 0 || summary.Unknown != 0 {
		t.Fatalf("empty window = %+v, want all zero", summary)
	}
	if summary.AutoRate != nil || summary.AssistedRate != nil {
		t.Fatalf("empty window rates = %v/%v, want null/null", summary.AutoRate, summary.AssistedRate)
	}
}

// An ASKING turn older than the grace window is abandoned.
func TestResolutionSummaryAbandoned(t *testing.T) {
	summary := summarizeResolution([]resolutionTurn{outcomeTurn(1, 0, resolvedState("ASKING"))},
		outcomesBase.Add(15*time.Minute))
	if summary.Abandoned != 1 || summary.Clarified != 0 || summary.ScoredTotal != 1 {
		t.Fatalf("summary = %+v, want abandoned=1 clarified=0 scored_total=1", summary)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 0) {
		t.Fatalf("auto_rate = %v, want 0 (not null)", summary.AutoRate)
	}
	if summary.TurnsTotal != summary.ScoredTotal+summary.Unknown+summary.Pending {
		t.Fatalf("identity broken: turns_total=%d scored_total=%d unknown=%d pending=%d",
			summary.TurnsTotal, summary.ScoredTotal, summary.Unknown, summary.Pending)
	}
}

// Only a later child linked by parent_turn_id marks the parent as clarified.
func TestResolutionSummaryParentLinkedClarified(t *testing.T) {
	parent := outcomeTurn(1, 0, resolvedState("ASKING"))
	child := outcomeTurn(1, 10, resolvedState("DONE"))
	child.ParentTurnID = &parent.TurnID
	summary := summarizeResolution([]resolutionTurn{parent, child}, outcomesBase.Add(20*time.Minute))
	if summary.Clarified != 1 || summary.Abandoned != 0 || summary.Solved != 1 || summary.ScoredTotal != 2 {
		t.Fatalf("summary = %+v, want clarified=1 abandoned=0 solved=1 scored_total=2", summary)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 0.5) {
		t.Fatalf("auto_rate = %v, want 0.5 (both rates share scored_total)", summary.AutoRate)
	}
	if summary.AssistedRate == nil || !almostEqual(*summary.AssistedRate, 1) {
		t.Fatalf("assisted_rate = %v, want 1", summary.AssistedRate)
	}
}

// A DONE in the same conversation without the parent link is not a clarification.
func TestResolutionSummaryUnrelatedDoneNotClarified(t *testing.T) {
	parent := outcomeTurn(1, 0, resolvedState("ASKING"))
	unrelated := outcomeTurn(1, 5, resolvedState("DONE"))
	summary := summarizeResolution([]resolutionTurn{parent, unrelated}, outcomesBase.Add(20*time.Minute))
	if summary.Clarified != 0 || summary.Abandoned != 1 || summary.Solved != 1 {
		t.Fatalf("summary = %+v, want clarified=0 abandoned=1 solved=1", summary)
	}
}

// The pending clock starts at the latest execution's finished_at, not at turn
// creation; an ASKING turn exactly at the ten-minute mark is no longer pending.
func TestResolutionSummaryPending(t *testing.T) {
	young := outcomeTurn(1, 0, resolvedState("ASKING"))
	young.FinishedAt = at(8)
	old := outcomeTurn(2, 5, resolvedState("ASKING"))
	summary := summarizeResolution([]resolutionTurn{young, old}, outcomesBase.Add(15*time.Minute))
	if summary.Pending != 1 || summary.Abandoned != 1 || summary.ScoredTotal != 1 {
		t.Fatalf("summary = %+v, want pending=1 abandoned=1 scored_total=1", summary)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 0) {
		t.Fatalf("auto_rate = %v, want 0", summary.AutoRate)
	}
	if summary.AssistedRate == nil || !almostEqual(*summary.AssistedRate, 0) {
		t.Fatalf("assisted_rate = %v, want 0", summary.AssistedRate)
	}
	if summary.TurnsTotal != summary.ScoredTotal+summary.Unknown+summary.Pending {
		t.Fatalf("identity broken: turns_total=%d scored_total=%d unknown=%d pending=%d",
			summary.TurnsTotal, summary.ScoredTotal, summary.Unknown, summary.Pending)
	}
}

// CANCELLED is its own bucket and no longer inflates failed.
func TestResolutionSummaryCancelledSplit(t *testing.T) {
	summary := summarizeResolution([]resolutionTurn{outcomeTurn(1, 0, resolvedState("CANCELLED"))},
		outcomesBase.Add(15*time.Minute))
	if summary.Cancelled != 1 || summary.Failed != 0 || summary.ScoredTotal != 1 {
		t.Fatalf("summary = %+v, want cancelled=1 failed=0 scored_total=1", summary)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 0) {
		t.Fatalf("auto_rate = %v, want 0", summary.AutoRate)
	}
}

// The re-ask counter is diagnostic only: both DONE turns stay solved, but only
// the one followed within five minutes is reported.
func TestResolutionSummaryReaskedWithin5m(t *testing.T) {
	summary := summarizeResolution([]resolutionTurn{
		outcomeTurn(1, 0, resolvedState("DONE")),
		outcomeTurn(1, 3, resolvedState("ASKING")),
		outcomeTurn(2, 0, resolvedState("DONE")),
		outcomeTurn(2, 10, resolvedState("ASKING")),
	}, outcomesBase.Add(30*time.Minute))
	if summary.ReaskedWithin5m != 1 {
		t.Fatalf("reasked_within_5m = %d, want 1", summary.ReaskedWithin5m)
	}
	if summary.Solved != 2 {
		t.Fatalf("solved = %d, want 2 (the diagnostic must not change the bucket)", summary.Solved)
	}
}

// A broken identity stays a 200 with metric_gap=true so the page is still
// usable; the anomaly is surfaced as data, not as an HTTP failure.
func TestResolutionSummaryMetricGapResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).respondOutcomes(recorder, 7, ResolutionSummary{TurnsTotal: 5, ScoredTotal: 3, Unknown: 1})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var payload ResolutionSummary
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if !payload.MetricGap {
		t.Fatalf("metric_gap = false, want true for turns_total=5 scored_total=3 unknown=1 pending=0")
	}
	if !strings.Contains(recorder.Body.String(), `"metric_gap":true`) {
		t.Fatalf("response does not serialize the flag explicitly: %s", recorder.Body.String())
	}
}

// A healthy summary must also serialize metric_gap=false, so the frontend can
// read the field unconditionally.
func TestResolutionSummaryMetricGapFalseIsExplicit(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Handler{}).respondOutcomes(recorder, 7, ResolutionSummary{TurnsTotal: 4, ScoredTotal: 3, Unknown: 1})
	if !strings.Contains(recorder.Body.String(), `"metric_gap":false`) {
		t.Fatalf("response omits the false flag: %s", recorder.Body.String())
	}
}

// Both rates must use scored_total, including clarified turns, and the JSON
// response must carry exactly the same float64 values.
func TestResolutionSummarySharedDenominator(t *testing.T) {
	parent := outcomeTurn(1, 0, resolvedState("ASKING"))
	child := outcomeTurn(1, 5, resolvedState("DONE"))
	child.ParentTurnID = &parent.TurnID
	summary := summarizeResolution([]resolutionTurn{
		parent,
		child,
		outcomeTurn(2, 0, resolvedState("DONE")),
		outcomeTurn(3, 0, resolvedState("FAILED")),
		outcomeTurn(4, 0, resolvedState("CANCELLED")),
		outcomeTurn(5, 0, resolvedState("ASKING")),
	}, outcomesBase.Add(20*time.Minute))
	if summary.Solved != 2 || summary.Clarified != 1 || summary.ScoredTotal != 6 {
		t.Fatalf("summary = %+v, want solved=2 clarified=1 scored_total=6", summary)
	}
	wantAuto := float64(summary.Solved) / float64(summary.ScoredTotal)
	wantAssisted := float64(summary.Solved+summary.Clarified) / float64(summary.ScoredTotal)
	if summary.AutoRate == nil || *summary.AutoRate != wantAuto {
		t.Fatalf("auto_rate = %v, want %v (solved/scored_total)", summary.AutoRate, wantAuto)
	}
	if summary.AssistedRate == nil || *summary.AssistedRate != wantAssisted {
		t.Fatalf("assisted_rate = %v, want %v ((solved+clarified)/scored_total)", summary.AssistedRate, wantAssisted)
	}
	if *summary.AutoRate == *summary.AssistedRate {
		t.Fatalf("rates are identical; clarified must move only the assisted numerator")
	}
	recorder := httptest.NewRecorder()
	(&Handler{}).respondOutcomes(recorder, 1, summary)
	var payload ResolutionSummary
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if payload.AutoRate == nil || *payload.AutoRate != wantAuto {
		t.Fatalf("JSON auto_rate = %v, want %v", payload.AutoRate, wantAuto)
	}
	if payload.AssistedRate == nil || *payload.AssistedRate != wantAssisted {
		t.Fatalf("JSON assisted_rate = %v, want %v", payload.AssistedRate, wantAssisted)
	}
}

// Every turn must land in exactly one settled bucket, unknown, or pending.
func TestResolutionSummaryIdentityHolds(t *testing.T) {
	pending := outcomeTurn(4, 0, resolvedState("ASKING"))
	pending.FinishedAt = at(12)
	parent := outcomeTurn(2, 0, resolvedState("ASKING"))
	child := outcomeTurn(2, 10, resolvedState("DONE"))
	child.ParentTurnID = &parent.TurnID
	summary := summarizeResolution([]resolutionTurn{
		outcomeTurn(1, 0, resolvedState("DONE")),
		outcomeTurn(1, 3, resolvedState("DONE")),
		parent,
		child,
		outcomeTurn(3, 0, resolvedState("ASKING")),
		pending,
		outcomeTurn(5, 0, resolvedState("FAILED")),
		outcomeTurn(6, 0, resolvedState("CANCELLED")),
		outcomeTurn(7, 0, nil),
	}, outcomesBase.Add(15*time.Minute))
	if summary.TurnsTotal != 9 {
		t.Fatalf("turns_total = %d, want 9", summary.TurnsTotal)
	}
	if summary.Solved != 3 || summary.Clarified != 1 || summary.Abandoned != 1 || summary.Pending != 1 ||
		summary.Failed != 1 || summary.Cancelled != 1 || summary.Unknown != 1 {
		t.Fatalf("summary = %+v, want solved=3 clarified=1 abandoned=1 pending=1 failed=1 cancelled=1 unknown=1", summary)
	}
	if summary.ScoredTotal != summary.Solved+summary.Clarified+summary.Abandoned+summary.Failed+summary.Cancelled {
		t.Fatalf("scored_total=%d does not equal the five buckets", summary.ScoredTotal)
	}
	if summary.TurnsTotal != summary.ScoredTotal+summary.Unknown+summary.Pending {
		t.Fatalf("identity broken: turns_total=%d scored_total=%d unknown=%d pending=%d",
			summary.TurnsTotal, summary.ScoredTotal, summary.Unknown, summary.Pending)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 3.0/7.0) {
		t.Fatalf("auto_rate = %v, want 3/7 (both rates share scored_total)", summary.AutoRate)
	}
	if summary.AssistedRate == nil || !almostEqual(*summary.AssistedRate, 4.0/7.0) {
		t.Fatalf("assisted_rate = %v, want 4/7", summary.AssistedRate)
	}
}
