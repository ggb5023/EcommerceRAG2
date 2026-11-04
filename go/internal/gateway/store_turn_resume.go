package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type resumeIdempotencyRecord struct {
	payloadHash string
	turnID      int64
	executionID int64
}

type resumedExecution struct {
	requestID   string
	status      string
	executionNo int64
}

func lockResumeTurn(ctx context.Context, tx pgx.Tx, tenantID, turnID, userID int64) (conversationID int64, shopID, turnStatus string, err error) {
	err = tx.QueryRow(ctx, `SELECT t.conversation_id,c.shop_id,t.status
		FROM turn t JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE t.tenant_id=$1 AND t.id=$2 AND c.user_id=$3 FOR UPDATE OF t,c`,
		tenantID, turnID, userID).Scan(&conversationID, &shopID, &turnStatus)
	return conversationID, shopID, turnStatus, err
}

func lookupResumeIdempotency(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64, storageKey string) (resumeIdempotencyRecord, bool, error) {
	var record resumeIdempotencyRecord
	err := tx.QueryRow(ctx, `SELECT payload_hash,turn_id,COALESCE(execution_id,
		(SELECT e.id FROM conversation_execution e
		 WHERE e.tenant_id=turn_idempotency.tenant_id AND e.turn_id=turn_idempotency.turn_id
		 ORDER BY e.execution_no ASC LIMIT 1))
		FROM turn_idempotency
		WHERE tenant_id=$1 AND conversation_id=$2 AND idempotency_key=$3`,
		tenantID, conversationID, storageKey).Scan(&record.payloadHash, &record.turnID, &record.executionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return resumeIdempotencyRecord{}, false, nil
	}
	if err != nil {
		return resumeIdempotencyRecord{}, false, err
	}
	return record, true, nil
}

func loadResumedExecution(ctx context.Context, tx pgx.Tx, tenantID, executionID int64) (resumedExecution, error) {
	var execution resumedExecution
	err := tx.QueryRow(ctx, `SELECT request_id,status,execution_no FROM conversation_execution
		WHERE tenant_id=$1 AND id=$2`, tenantID, executionID).Scan(
		&execution.requestID, &execution.status, &execution.executionNo)
	return execution, err
}

func loadAskingExecution(ctx context.Context, tx pgx.Tx, tenantID, turnID int64) (int64, int64, error) {
	var executionID, executionNo int64
	err := tx.QueryRow(ctx, `SELECT id,execution_no FROM conversation_execution
		WHERE tenant_id=$1 AND turn_id=$2 AND status='ASKING'
		ORDER BY execution_no DESC LIMIT 1 FOR UPDATE`, tenantID, turnID).Scan(&executionID, &executionNo)
	return executionID, executionNo, err
}

// persistResume creates the new execution, reactivates the turn, records the
// prefixed idempotency key and appends the user message in one transaction.
func persistResume(ctx context.Context, tx pgx.Tx, tenantID, turnID, conversationID, executionNo int64,
	requestID, permissionRevision string, deadline time.Time, mock bool,
	storageKey, payloadHash, query string) (executionID int64, err error) {
	if err = tx.QueryRow(ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,deadline_at,permission_revision)
			VALUES($1,$2,$3,$4,'EXECUTING',$7,$5,$6) RETURNING id`,
		tenantID, turnID, executionNo, requestID, deadline, permissionRevision, mock).Scan(&executionID); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE turn SET status='EXECUTING',answer=NULL WHERE tenant_id=$1 AND id=$2`,
		tenantID, turnID); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO turn_idempotency(tenant_id,conversation_id,idempotency_key,payload_hash,turn_id,execution_id)
			VALUES($1,$2,$3,$4,$5,$6)`, tenantID, conversationID, storageKey, payloadHash, turnID, executionID); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO message(tenant_id,turn_id,role,content) VALUES($1,$2,'user',$3)`,
		tenantID, turnID, query); err != nil {
		return 0, err
	}
	return executionID, nil
}
