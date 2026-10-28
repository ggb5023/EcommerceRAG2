package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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
	_, err = tx.Exec(eventCtx, `INSERT INTO sse_event(tenant_id,execution_id,sequence,event_type,data_json) VALUES($1,$2,$3,$4,$5::jsonb)`, ident.tenantID, ident.executionID, sequence, kind, string(body))
	if err != nil {
		return err
	}
	return tx.Commit(eventCtx)
}

// recordToolCall appends one tool_call_trace row. A failed trace write never
// fails the turn: the caller ignores the error, exactly as the inline version did.
func recordToolCall(ctx context.Context, db *pgxpool.Pool, tenantID int64, requestID, toolName, status string, degraded bool) error {
	_, err := db.Exec(ctx, `INSERT INTO tool_call_trace(tenant_id,request_id,tool_name,result_status,degraded)
		VALUES($1,$2,$3,$4,$5)`, tenantID, requestID, toolName, status, degraded)
	return err
}

// finishTurn writes the terminal state unless the execution already ended.
// Cancellation wins over the requested state, exactly as the inline version did.
func finishTurn(ctx context.Context, db *pgxpool.Pool, ident turnEventIdentity, state, code, answer string, mock bool) {
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
		conversationID: ident.conversationID, executionNo: ident.executionNo, requestID: ident.requestID, mock: mock,
	}, state, code, answer)
	if err == nil {
		err = tx.Commit(finishCtx)
	}
	if err != nil {
		log.Printf("finish execution transaction failed")
	}
}
