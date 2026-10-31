package gateway

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ggb5023/EcommerceRAG/go/internal/admin"
)

// recordTurnDecision writes the explanatory row for one turn in its own short
// transaction, mirroring finishTurn. It never fails the turn: the row is
// explanatory data, so any problem is logged and the request continues.
//
// rejected currently carries evidence ids only; the per-item reason (disclosure
// class, permission, validity window, contract breach) is B6's second step and
// must not change the decision functions' signatures to get there.
func recordTurnDecision(ctx context.Context, db *pgxpool.Pool, ident turnEventIdentity,
	state, code string, chosen, rejected []string) {
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

	encode := func(ids []string) []byte {
		if len(ids) == 0 {
			return []byte("[]")
		}
		payload, marshalErr := json.Marshal(ids)
		if marshalErr != nil {
			return []byte("[]")
		}
		return payload
	}
	decision := admin.TurnDecision{
		TenantID:       ident.tenantID,
		ConversationID: strconv.FormatInt(ident.conversationID, 10),
		TurnID:         strconv.FormatInt(ident.turnID, 10),
		Chosen:         encode(chosen),
		Rejected:       encode(rejected),
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
