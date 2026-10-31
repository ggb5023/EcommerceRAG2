package admin

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// TurnDecision is one row of turn_decision: what the turn chose, what it threw
// away and why, and how it ended. JSON fields arrive already serialized so this
// package never interprets them.
type TurnDecision struct {
	TenantID       int64
	ConversationID string
	TurnID         string
	State          string
	Chosen         []byte
	Rejected       []byte
	Strength       *string
	FailureCode    *string
	Confidence     *float64
}

// AppendTurnDecision records the decision for one turn inside the caller's
// transaction. A repeated write for the same turn is ignored: the first record
// wins, and a retry never fails the turn.
func (s *Store) AppendTurnDecision(ctx context.Context, tx pgx.Tx, decision TurnDecision) error {
	if tx == nil {
		return ErrStoreNeedsTransaction
	}
	if len(decision.Chosen) == 0 {
		decision.Chosen = []byte("[]")
	}
	if len(decision.Rejected) == 0 {
		decision.Rejected = []byte("[]")
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO turn_decision(tenant_id,conversation_id,turn_id,state,chosen,rejected,strength,failure_code,confidence)
		 VALUES($1,$2,$3,$4,$5::jsonb,$6::jsonb,$7,$8,$9)
		 ON CONFLICT (tenant_id,conversation_id,turn_id) DO NOTHING`,
		decision.TenantID, decision.ConversationID, decision.TurnID, decision.State,
		string(decision.Chosen), string(decision.Rejected),
		decision.Strength, decision.FailureCode, decision.Confidence)
	return err
}
