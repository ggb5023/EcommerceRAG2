package domain

import "testing"

func TestCanCopyReply(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     string
		behavior  string
		citations int
		facts     int
		mock      bool
		canCopy   bool
		reason    string
	}{
		{name: "answer with citation", state: "DONE", behavior: "REPLY_BEHAVIOR_ANSWER", citations: 1, canCopy: true},
		{name: "short answer enum", state: "DONE", behavior: "ANSWER", citations: 2, canCopy: true},
		{name: "answer with fact snapshot", state: "DONE", behavior: "ANSWER", facts: 1, canCopy: true},
		{name: "answer without citation or fact", state: "DONE", behavior: "ANSWER", reason: "no_citable_evidence"},
		{name: "clarify with fact", state: "DONE", behavior: "REPLY_BEHAVIOR_CLARIFY", facts: 1, reason: "needs_clarification"},
		{name: "clarify", state: "DONE", behavior: "REPLY_BEHAVIOR_CLARIFY", citations: 1, reason: "needs_clarification"},
		{name: "asking clarify", state: "ASKING", behavior: "REPLY_BEHAVIOR_CLARIFY", reason: "needs_clarification"},
		{name: "asking without behavior", state: "ASKING", reason: "needs_clarification"},
		{name: "handoff", state: "DONE", behavior: "REPLY_BEHAVIOR_HANDOFF", citations: 1, reason: "not_answer"},
		{name: "failed", state: "FAILED", behavior: "ANSWER", citations: 1, reason: "incomplete"},
		{name: "mock answer", state: "DONE", behavior: "ANSWER", citations: 1, mock: true, reason: "mock"},
		{name: "mock fact answer", state: "DONE", behavior: "ANSWER", facts: 1, mock: true, reason: "mock"},
		{name: "empty behavior remains answer", state: "DONE", citations: 1, canCopy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := CanCopyReply(tc.state, tc.behavior, tc.citations, tc.facts, tc.mock)
			if got != tc.canCopy || reason != tc.reason {
				t.Fatalf("CanCopyReply() = %v/%q, want %v/%q", got, reason, tc.canCopy, tc.reason)
			}
		})
	}
}
