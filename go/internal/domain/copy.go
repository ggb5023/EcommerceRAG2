package domain

import "strings"

// CanCopyReply is the single 0005 decision for an assistant reply.
//
// nonMockFactCount counts fact snapshots with is_mock=false; a mock fact
// never authorizes copying (decision 0041).
// Behavior is the protobuf enum name (for example REPLY_BEHAVIOR_ANSWER) or
// its short form; an empty behavior preserves the legacy ANSWER path used by
// pre-decision rows. The returned reason is the stable blocked_reason value.
func CanCopyReply(state, behavior string, citationCount, nonMockFactCount int, mock bool) (bool, string) {
	kind := strings.ToUpper(strings.TrimPrefix(behavior, "REPLY_BEHAVIOR_"))
	if kind == "CLARIFY" || state == "ASKING" {
		return false, "needs_clarification"
	}
	if state != "DONE" {
		return false, "incomplete"
	}
	if kind == "HANDOFF" {
		return false, "not_answer"
	}
	if mock {
		return false, "mock"
	}
	if citationCount < 1 && nonMockFactCount < 1 {
		return false, "no_citable_evidence"
	}
	return true, ""
}
