package admin

import (
	"testing"
	"time"
)

func TestKnowledgeGapsNormalization(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Refund   Status?! ", "refund status"},
		{"退款，怎么办！", "退款怎么办"},
		{"A\tB\nC", "a b c"},
		{"...", ""},
	}
	for _, tc := range cases {
		if got := normalizeGapQuery(tc.in); got != tc.want {
			t.Fatalf("normalizeGapQuery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Only abandoned and failed turns enter the gap list. Pending, clarified,
// cancelled and unknown turns are excluded because they do not mean missing
// content.
func TestKnowledgeGapsBuckets(t *testing.T) {
	abandoned := outcomeTurn(1, 0, resolvedState("ASKING"))
	abandoned.UserQuery = "abandoned question"
	failed := outcomeTurn(2, 0, resolvedState("FAILED"))
	failed.UserQuery = "failed question"
	pending := outcomeTurn(3, 19, resolvedState("ASKING"))
	pending.UserQuery = "pending question"
	clarified := outcomeTurn(4, 0, resolvedState("ASKING"))
	clarified.UserQuery = "clarified question"
	child := outcomeTurn(4, 5, resolvedState("DONE"))
	child.ParentTurnID = &clarified.TurnID
	child.UserQuery = "child answer"
	cancelled := outcomeTurn(5, 0, resolvedState("CANCELLED"))
	cancelled.UserQuery = "cancelled question"
	unknown := outcomeTurn(6, 0, nil)
	unknown.UserQuery = "unknown question"

	items := summarizeKnowledgeGaps([]resolutionTurn{
		abandoned, failed, pending, clarified, child, cancelled, unknown,
	}, 1, outcomesBase.Add(20*time.Minute))
	if len(items) != 2 {
		t.Fatalf("items = %+v, want abandoned and failed only", items)
	}
	got := map[string]string{}
	for _, item := range items {
		got[item.Normalized] = item.Reason
	}
	if got["abandoned question"] != "abandoned" {
		t.Fatalf("abandoned reason = %q, want abandoned", got["abandoned question"])
	}
	if got["failed question"] != "failed" {
		t.Fatalf("failed reason = %q, want failed", got["failed question"])
	}
	for _, excluded := range []string{"pending question", "clarified question", "cancelled question", "unknown question", "child answer"} {
		if _, ok := got[excluded]; ok {
			t.Fatalf("%q must not enter the gap list: %+v", excluded, items)
		}
	}
}

// Aggregation keeps the latest original wording, the latest seen time, and the
// latest reason for the normalized key.
func TestKnowledgeGapsAggregationAndLastSeen(t *testing.T) {
	first := outcomeTurn(1, 0, resolvedState("FAILED"))
	first.UserQuery = "Refund status?"
	second := outcomeTurn(1, 10, resolvedState("FAILED"))
	second.UserQuery = "refund status."
	latest := outcomeTurn(1, 15, resolvedState("ASKING"))
	latest.UserQuery = "REFUND STATUS"

	items := summarizeKnowledgeGaps([]resolutionTurn{first, second, latest}, 1, outcomesBase.Add(30*time.Minute))
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one normalized cluster", items)
	}
	item := items[0]
	if item.Normalized != "refund status" || item.Count != 3 {
		t.Fatalf("item = %+v, want normalized=refund status count=3", item)
	}
	if item.Query != "REFUND STATUS" {
		t.Fatalf("query = %q, want the latest original wording", item.Query)
	}
	if !item.LastSeen.Equal(outcomesBase.Add(15 * time.Minute)) {
		t.Fatalf("last_seen = %v, want the latest turn time", item.LastSeen)
	}
	if item.Reason != "abandoned" {
		t.Fatalf("reason = %q, want the latest turn's abandoned", item.Reason)
	}
}

func TestKnowledgeGapsMinCount(t *testing.T) {
	alphaOne := outcomeTurn(1, 0, resolvedState("FAILED"))
	alphaOne.UserQuery = "alpha"
	alphaTwo := outcomeTurn(2, 1, resolvedState("FAILED"))
	alphaTwo.UserQuery = "Alpha!"
	beta := outcomeTurn(3, 0, resolvedState("FAILED"))
	beta.UserQuery = "beta"

	items := summarizeKnowledgeGaps([]resolutionTurn{alphaOne, alphaTwo, beta}, 2, outcomesBase.Add(20*time.Minute))
	if len(items) != 1 || items[0].Normalized != "alpha" || items[0].Count != 2 {
		t.Fatalf("items = %+v, want only alpha with count 2", items)
	}
}

func TestKnowledgeGapsEmptyWindow(t *testing.T) {
	if items := summarizeKnowledgeGaps(nil, 1, outcomesBase); len(items) != 0 {
		t.Fatalf("items = %+v, want empty", items)
	}
}
