package domain

import (
	"strings"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

// ClarificationDecision is the pure routing outcome for an ambiguous question.
type ClarificationDecision struct {
	Needed bool
	Text   string
}

// DecideClarification reports whether the routed turn must stop and ask the user
// to be more specific. It is pure: it reads the routed response only.
func DecideClarification(routed *ragv1.UnderstandResponse, snapshotFallback bool) ClarificationDecision {
	if snapshotFallback {
		return ClarificationDecision{}
	}
	if !(routed.Intent == "clarification" || strings.TrimSpace(routed.Clarification) != "" && routed.Confidence < 0.5) {
		return ClarificationDecision{}
	}
	text := routed.Clarification
	if text == "" {
		text = "请补充更多问题细节。"
	}
	return ClarificationDecision{Needed: true, Text: text}
}
