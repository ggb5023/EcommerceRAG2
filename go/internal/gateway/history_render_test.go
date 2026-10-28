package gateway

import (
	"strings"
	"testing"
)

// The database read itself is not unit-tested here; this pins the rendering and
// ordering contract that runTurn depends on.
func TestBoundedHistoryKeepsAllMessagesInOrder(t *testing.T) {
	history := boundedHistory([]historyMessage{
		{Role: "user", Content: "第一个问题"},
		{Role: "assistant", Content: "第一个回答"},
		{Role: "user", Content: "第二个问题"},
	})
	for _, want := range []string{"第一个问题", "第一个回答", "第二个问题"} {
		if !strings.Contains(history, want) {
			t.Fatalf("history lost %q: %s", want, history)
		}
	}
	first := strings.Index(history, "第一个问题")
	second := strings.Index(history, "第一个回答")
	third := strings.Index(history, "第二个问题")
	if !(first < second && second < third) {
		t.Fatalf("history order changed: %s", history)
	}
}

func TestBoundedHistoryDropsBlankAndUnknownRoles(t *testing.T) {
	history := boundedHistory([]historyMessage{
		{Role: "user", Content: "  "},
		{Role: "system", Content: "内部提示"},
		{Role: "user", Content: "有效问题"},
	})
	if strings.Contains(history, "内部提示") || strings.Contains(history, "有效问题") == false {
		t.Fatalf("unexpected history rendering: %s", history)
	}
}
