package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
)

// turnEventIdentity names the rows one streamed event belongs to. Stage code
// receives it as data so the persistence helpers never touch gateway fields.
type turnEventIdentity struct {
	tenantID       int64
	executionID    int64
	turnID         int64
	conversationID int64
	executionNo    int64
	requestID      string
}

// writeTurnEvent appends one SSE event and stamps the per-turn envelope fields.
// It refuses to append once the execution reached a terminal state.
func writeTurnEvent(db *pgxpool.Pool, ident turnEventIdentity, kind string, payload any) error {
	eventCtx, eventCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer eventCancel()
	tx, err := db.Begin(eventCtx)
	if err != nil {
		return err
	}
	defer tx.Rollback(eventCtx)
	var current string
	if err = tx.QueryRow(eventCtx, `SELECT status FROM conversation_execution WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, ident.tenantID, ident.executionID).Scan(&current); err != nil {
		return err
	}
	if terminal(current) {
		return errors.New("execution already terminated")
	}
	var sequence int64
	if err = tx.QueryRow(eventCtx, `SELECT COALESCE(MAX(sequence),0)+1 FROM sse_event WHERE tenant_id=$1 AND execution_id=$2`, ident.tenantID, ident.executionID).Scan(&sequence); err != nil {
		return err
	}
	if object, ok := payload.(map[string]any); ok {
		object["seq"] = sequence
		object["request_id"] = ident.requestID
		object["turn_id"] = strconv.FormatInt(ident.turnID, 10)
		object["conversation_id"] = strconv.FormatInt(ident.conversationID, 10)
		object["execution_no"] = ident.executionNo
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err = insertSSEEvent(eventCtx, tx, ident.tenantID, ident.executionID, sequence, kind, body); err != nil {
		return err
	}
	return tx.Commit(eventCtx)
}

// recordToolCall appends one tool_call_trace row. A failed trace write never
// fails the turn: the caller ignores the error, exactly as the inline version did.
func recordToolCall(ctx context.Context, db *pgxpool.Pool, tenantID int64, requestID, toolName, status string, degraded bool) error {
	return insertToolCallTrace(ctx, db, tenantID, requestID, toolName, status, degraded)
}

// finishTurn writes the terminal state, the turn status and the decision row in
// one transaction. Cancellation wins over the requested state, exactly as the
// inline version did, and the ledger records that final state too.
func finishTurn(ctx context.Context, db *pgxpool.Pool, ident turnEventIdentity, state, code, answer, behavior string, mock bool,
	chosen []string, rejected []domain.RejectedEvidence) {
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	tx, err := db.Begin(finishCtx)
	if err != nil {
		log.Printf("finish transaction unavailable")
		return
	}
	defer tx.Rollback(finishCtx)
	var current string
	err = tx.QueryRow(finishCtx, `SELECT status FROM conversation_execution WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, ident.tenantID, ident.executionID).Scan(&current)
	if err != nil || terminal(current) {
		return
	}
	if current == "CANCEL_REQUESTED" || errors.Is(ctx.Err(), context.Canceled) {
		state = "CANCELLED"
		code = "cancelled"
		answer = ""
	}
	if state == "DONE" && ctx.Err() != nil {
		state, code = grpcFailure(ctx, ctx.Err())
		answer = ""
	}
	err = persistExecutionTerminal(finishCtx, tx, executionIdentity{
		tenantID: ident.tenantID, executionID: ident.executionID, turnID: ident.turnID,
		conversationID: ident.conversationID, executionNo: ident.executionNo, requestID: ident.requestID, behavior: behavior, mock: mock,
	}, state, code, answer)
	if err == nil {
		err = appendTurnDecision(finishCtx, tx, ident, state, code, chosen, rejected)
	}
	if err == nil {
		err = tx.Commit(finishCtx)
	}
	if err != nil {
		log.Printf("finish execution transaction failed")
	}
}

// turnTrace is the data the terminal transaction writes. Retrieval sets the
// rejected side once its result is known; generation only adds chosen.
type turnTrace struct {
	chosen   []string
	rejected []domain.RejectedEvidence
}

func (trace *turnTrace) setTrace(rejected []domain.RejectedEvidence) {
	trace.rejected = rejected
}

func (trace *turnTrace) setChosen(chosen []string) {
	trace.chosen = chosen
}

// newTurnSink binds the persistence helpers to one execution so the orchestrator
// only sees closures: write an event, finish the turn, set behavior and update
// the trace. setTrace sets rejected once after retrieval; setChosen only adds
// the answer citations. The decision row is written inside finishTurn's
// transaction.
func newTurnSink(ctx context.Context, db *pgxpool.Pool, ident turnEventIdentity, mock bool) (
	func(string, any) error, func(string, string, string), func(string),
	func([]domain.RejectedEvidence), func([]string)) {
	write := func(kind string, payload any) error { return writeTurnEvent(db, ident, kind, payload) }
	trace := &turnTrace{}
	var behavior string
	finish := func(state, code, answer string) {
		finishTurn(ctx, db, ident, state, code, answer, behavior, mock, trace.chosen, trace.rejected)
	}
	setBehavior := func(value string) { behavior = value }
	return write, finish, setBehavior, trace.setTrace, trace.setChosen
}
