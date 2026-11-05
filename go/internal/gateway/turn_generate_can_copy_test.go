package gateway

import (
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

func TestTerminalCanCopy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     string
		kind      string
		citations int
		mock      bool
		canCopy   bool
		reason    string
	}{
		{"answer with citation", "DONE", "ANSWER", 1, false, true, ""},
		{"answer without citation", "DONE", "ANSWER", 0, false, false, "no_citable_evidence"},
		{"clarify", "DONE", "CLARIFY", 1, false, false, "needs_clarification"},
		{"handoff", "DONE", "HANDOFF", 1, false, false, "not_answer"},
		{"failed", "FAILED", "ANSWER", 1, false, false, "incomplete"},
		{"mock answer", "DONE", "ANSWER", 1, true, false, "mock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := terminalCanCopy(tc.state, tc.kind, tc.citations, tc.mock)
			if got != tc.canCopy || reason != tc.reason {
				t.Fatalf("terminalCanCopy() = %v/%q, want %v/%q", got, reason, tc.canCopy, tc.reason)
			}
		})
	}
}

func TestFinishGenerationPassesReplyBehavior(t *testing.T) {
	for _, tc := range []struct {
		name         string
		decision     *ragv1.ReplyDecision
		wantState    string
		wantBehavior string
	}{
		{"answer", &ragv1.ReplyDecision{Behavior: ragv1.ReplyBehavior_REPLY_BEHAVIOR_ANSWER}, "DONE", "ANSWER"},
		{"clarify", &ragv1.ReplyDecision{Behavior: ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY}, "DONE", "CLARIFY"},
		{"handoff", &ragv1.ReplyDecision{Behavior: ragv1.ReplyBehavior_REPLY_BEHAVIOR_HANDOFF}, "FAILED", "HANDOFF"},
		{"legacy missing decision", nil, "DONE", "ANSWER"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state, behavior string
			finish := func(value, _, _ string) { state = value }
			setBehavior := func(value string) { behavior = value }
			finishGeneration(finish, setBehavior, tc.decision, "answer")
			if state != tc.wantState || behavior != tc.wantBehavior {
				t.Fatalf("finishGeneration() = %s/%s, want %s/%s", state, behavior, tc.wantState, tc.wantBehavior)
			}
		})
	}
}
