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
	ExecutionID    int64
	ConversationID string
	TurnID         string
	State          string
	Chosen         []byte
	Rejected       []byte
	Strength       *string
	FailureCode    *string
	Confidence     *float64
}

// AppendTurnDecision records the decision for one execution inside the caller's
// transaction. The execution key makes a replayed terminal write overwrite the
// same row instead of silently keeping an earlier state.
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
		`INSERT INTO turn_decision(tenant_id,execution_id,conversation_id,turn_id,state,chosen,rejected,strength,failure_code,confidence)
		 VALUES($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,$9,$10)
		 ON CONFLICT (tenant_id,execution_id) DO UPDATE SET
		   state=EXCLUDED.state, chosen=EXCLUDED.chosen, rejected=EXCLUDED.rejected,
		   strength=EXCLUDED.strength, failure_code=EXCLUDED.failure_code, confidence=EXCLUDED.confidence`,
		decision.TenantID, decision.ExecutionID, decision.ConversationID, decision.TurnID, decision.State,
		string(decision.Chosen), string(decision.Rejected),
		decision.Strength, decision.FailureCode, decision.Confidence)
	return err
}
