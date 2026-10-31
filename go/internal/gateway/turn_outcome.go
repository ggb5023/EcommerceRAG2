package gateway

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ggb5023/EcommerceRAG/go/internal/admin"
	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
)

// recordTurnDecision writes the explanatory row for one turn in its own short
// transaction, mirroring finishTurn. It never fails the turn: the row is
// explanatory data, so any problem is logged and the request continues.
//
// chosen carries the citation ids the answer used; rejected carries one
// {"id","reason"} object per candidate the filter dropped.
func recordTurnDecision(ctx context.Context, db *pgxpool.Pool, ident turnEventIdentity,
	state, code string, chosen []string, rejected []domain.RejectedEvidence) {
	if db == nil {
		return
	}
	decisionCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := db.Begin(decisionCtx)
	if err != nil {
		log.Printf("turn decision transaction unavailable")
		return
	}
	defer func() { _ = tx.Rollback(decisionCtx) }()

	decision := admin.TurnDecision{
		TenantID:       ident.tenantID,
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
	if err = store.AppendTurnDecision(decisionCtx, tx, decision); err != nil {
		log.Printf("turn decision not recorded: %v", err)
		return
	}
	if err = tx.Commit(decisionCtx); err != nil {
		log.Printf("turn decision commit failed")
	}
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
