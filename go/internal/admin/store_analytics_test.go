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

func resolvedState(value string) *string { return &value }

func outcomeTurn(conversation int64, minute int, state *string) resolutionTurn {
	return resolutionTurn{ConversationID: conversation, CreatedAt: outcomesBase.Add(time.Duration(minute) * time.Minute), State: state}
}

func almostEqual(got, want float64) bool {
	return math.Abs(got-want) < 1e-9
}

func TestResolutionSummaryEmptyWindow(t *testing.T) {
	summary := summarizeResolution(nil)
	if summary.TurnsTotal != 0 || summary.ScoredTotal != 0 || summary.Solved != 0 || summary.Clarified != 0 ||
		summary.Abandoned != 0 || summary.Failed != 0 || summary.Unknown != 0 {
		t.Fatalf("empty window = %+v, want all zero", summary)
	}
	if summary.AutoRate != nil || summary.AssistedRate != nil {
		t.Fatalf("empty window rates = %v/%v, want null/null", summary.AutoRate, summary.AssistedRate)
	}
}

// An ASKING turn whose conversation never reaches DONE is scored as abandoned:
// excluding it from the denominator is what inflated the old rate.
func TestResolutionSummaryAbandoned(t *testing.T) {
	summary := summarizeResolution([]resolutionTurn{outcomeTurn(1, 0, resolvedState("ASKING"))})
	if summary.Abandoned != 1 || summary.Clarified != 0 || summary.ScoredTotal != 1 {
		t.Fatalf("summary = %+v, want abandoned=1 clarified=0 scored_total=1", summary)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 0) {
		t.Fatalf("auto_rate = %v, want 0 (not null)", summary.AutoRate)
	}
	if summary.TurnsTotal != summary.ScoredTotal+summary.Unknown {
		t.Fatalf("identity broken: turns_total=%d scored_total=%d unknown=%d", summary.TurnsTotal, summary.ScoredTotal, summary.Unknown)
	}
}

// An ASKING turn followed by DONE in the same conversation is clarified, not
// abandoned, and the later DONE is solved.
func TestResolutionSummaryClarified(t *testing.T) {
	summary := summarizeResolution([]resolutionTurn{
		outcomeTurn(1, 0, resolvedState("ASKING")),
		outcomeTurn(1, 10, resolvedState("DONE")),
	})
	if summary.Clarified != 1 || summary.Abandoned != 0 || summary.Solved != 1 || summary.ScoredTotal != 2 {
		t.Fatalf("summary = %+v, want clarified=1 abandoned=0 solved=1 scored_total=2", summary)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 0.5) {
		t.Fatalf("auto_rate = %v, want 0.5", summary.AutoRate)
	}
	if summary.AssistedRate == nil || !almostEqual(*summary.AssistedRate, 1) {
		t.Fatalf("assisted_rate = %v, want 1", summary.AssistedRate)
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
	})
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
		t.Fatalf("metric_gap = false, want true for turns_total=5 scored_total=3 unknown=1")
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

// Every turn with a decision row must land in exactly one scored bucket; only
// turns without a row may be unknown. The four buckets must sum to scored_total.
func TestResolutionSummaryIdentityHolds(t *testing.T) {
	summary := summarizeResolution([]resolutionTurn{
		// A DONE followed by another turn is still solved under the five-bucket
		// definition; there is no half-counted turn.
		outcomeTurn(1, 0, resolvedState("DONE")),
		outcomeTurn(1, 3, resolvedState("DONE")),
		outcomeTurn(2, 0, resolvedState("ASKING")),
		outcomeTurn(2, 10, resolvedState("DONE")),
		outcomeTurn(3, 0, resolvedState("ASKING")),
		outcomeTurn(4, 0, resolvedState("FAILED")),
		outcomeTurn(5, 0, nil),
	})
	if summary.TurnsTotal != 7 {
		t.Fatalf("turns_total = %d, want 7", summary.TurnsTotal)
	}
	if summary.Solved != 3 || summary.Clarified != 1 || summary.Abandoned != 1 || summary.Failed != 1 || summary.Unknown != 1 {
		t.Fatalf("summary = %+v, want solved=3 clarified=1 abandoned=1 failed=1 unknown=1", summary)
	}
	if summary.ScoredTotal != summary.Solved+summary.Clarified+summary.Abandoned+summary.Failed {
		t.Fatalf("scored_total=%d does not equal the four buckets", summary.ScoredTotal)
	}
	if summary.TurnsTotal != summary.ScoredTotal+summary.Unknown {
		t.Fatalf("identity broken: turns_total=%d scored_total=%d unknown=%d", summary.TurnsTotal, summary.ScoredTotal, summary.Unknown)
	}
	if summary.AutoRate == nil || !almostEqual(*summary.AutoRate, 3.0/6.0) {
		t.Fatalf("auto_rate = %v, want 3/6", summary.AutoRate)
	}
	if summary.AssistedRate == nil || !almostEqual(*summary.AssistedRate, 4.0/6.0) {
		t.Fatalf("assisted_rate = %v, want 4/6", summary.AssistedRate)
	}
}
