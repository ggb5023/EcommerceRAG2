package gateway

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/ggb5023/EcommerceRAG/go/internal/admin"
	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
)

// appendTurnDecision writes the explanatory row inside the caller's terminal
// transaction. A failure aborts that transaction: the execution, the turn and
// the decision row must become visible together.
//
// chosen carries the citation ids the answer used; rejected carries one
// {"id","reason"} object per candidate the filter dropped.
func appendTurnDecision(ctx context.Context, tx pgx.Tx, ident turnEventIdentity,
	state, code string, chosen []string, rejected []domain.RejectedEvidence) error {
	decision := admin.TurnDecision{
		TenantID:       ident.tenantID,
		ExecutionID:    ident.executionID,
		ConversationID: strconv.FormatInt(ident.conversationID, 10),
		TurnID:         strconv.FormatInt(ident.turnID, 10),
		State:          state,
		Chosen:         encodeDecisionList(chosen),
		Rejected:       encodeDecisionList(rejected),
	}
	if state == "FAILED" && code != "" {
		decision.FailureCode = &code
	}
	store := admin.Store{}
	return store.AppendTurnDecision(ctx, tx, decision)
}

// encodeDecisionList always writes a JSON array, so an empty list stays [] and
// never becomes SQL NULL.
func encodeDecisionList[T any](items []T) []byte {
	if len(items) == 0 {
		return []byte("[]")
	}
	payload, marshalErr := json.Marshal(items)
	if marshalErr != nil {
		return []byte("[]")
	}
	return payload
}
