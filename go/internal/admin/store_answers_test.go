package admin

import "testing"

// The review page must show the decision Go stored. Only a legacy NULL row
// falls back to the terminal-state inference; otherwise a DONE CLARIFY row
// would be displayed as ANSWER again.
func TestAnswerBehaviorPrefersStoredDecision(t *testing.T) {
	for _, tc := range []struct {
		name     string
		behavior string
		state    string
		want     string
	}{
		{"done clarify", "CLARIFY", "DONE", "CLARIFY"},
		{"done answer", "ANSWER", "DONE", "ANSWER"},
		{"failed without behavior falls back to handoff", "", "FAILED", "HANDOFF"},
		{"legacy done row falls back to answer", "", "DONE", "ANSWER"},
		{"legacy asking row falls back to clarify", "", "ASKING", "CLARIFY"},
		{"full enum behavior is normalized", "REPLY_BEHAVIOR_ANSWER", "DONE", "ANSWER"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := answerBehavior(tc.behavior, tc.state); got != tc.want {
				t.Fatalf("answerBehavior(%q, %q) = %q, want %q", tc.behavior, tc.state, got, tc.want)
			}
		})
	}
}
