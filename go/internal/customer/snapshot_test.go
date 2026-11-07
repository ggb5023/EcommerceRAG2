package customer

import (
	"strings"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestMarshalCitationSnapshotAlwaysWritesAnArray(t *testing.T) {
	for _, citations := range [][]*ragv1.Evidence{nil, {}} {
		raw, err := marshalCitationSnapshot(citations)
		if err != nil || string(raw) != "[]" {
			t.Fatalf("citations %v -> %s, %v", citations, raw, err)
		}
	}
	raw, err := marshalCitationSnapshot([]*ragv1.Evidence{{Id: "e1", Rank: 1}})
	if err != nil || !strings.Contains(string(raw), `"id":"e1"`) {
		t.Fatalf("unexpected snapshot %s, %v", raw, err)
	}
}

func TestNonMockFactCount(t *testing.T) {
	cases := map[string]int{
		`[{"tool_name":"get_stock","is_mock":false},{"tool_name":"get_price","is_mock":true},{"is_mock":false}]`: 1,
		`[{"tool_name":"get_price","is_mock":true}]`:                                                             0,
		`null`:     0,
		`not-json`: 0,
		`[]`:       0,
	}
	for raw, want := range cases {
		if got := nonMockFactCount([]byte(raw)); got != want {
			t.Fatalf("nonMockFactCount(%s)=%d want %d", raw, got, want)
		}
	}
}

func TestShouldRevalidateFactsOnlyForAnswers(t *testing.T) {
	facts := []*ragv1.Fact{{ToolName: "get_order_status"}}
	cases := []struct {
		behavior ragv1.ReplyBehavior
		facts    []*ragv1.Fact
		want     bool
	}{
		{ragv1.ReplyBehavior_REPLY_BEHAVIOR_ANSWER, facts, true},
		{ragv1.ReplyBehavior_REPLY_BEHAVIOR_ANSWER, nil, false},
		{ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY, facts, false},
		{ragv1.ReplyBehavior_REPLY_BEHAVIOR_HANDOFF, facts, false},
	}
	for _, tc := range cases {
		if got := shouldRevalidateFacts(tc.behavior, tc.facts); got != tc.want {
			t.Fatalf("shouldRevalidateFacts(%v, %v)=%v want %v", tc.behavior, tc.facts, got, tc.want)
		}
	}
}

func TestPublicCopyDecision(t *testing.T) {
	if copyable, reason := publicCopyDecision("DONE", "REPLY_BEHAVIOR_ANSWER", 0, 1, false, true); copyable || reason != "not_copyable" {
		t.Fatalf("redacted copy=%v/%q", copyable, reason)
	}
	if copyable, reason := publicCopyDecision("DONE", "REPLY_BEHAVIOR_ANSWER", 0, 1, false, false); !copyable || reason != "" {
		t.Fatalf("fact copy=%v/%q", copyable, reason)
	}
	if copyable, reason := publicCopyDecision("DONE", "REPLY_BEHAVIOR_ANSWER", 0, 0, false, false); copyable || reason != "no_citable_evidence" {
		t.Fatalf("unsupported copy=%v/%q", copyable, reason)
	}
}
