package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func lockConversationShop(ctx context.Context, tx pgx.Tx, tenantID, userID, conversationID int64) (string, error) {
	var shopID string
	err := tx.QueryRow(ctx, `SELECT shop_id FROM conversation
		WHERE tenant_id=$1 AND user_id=$2 AND id=$3 FOR UPDATE`,
		tenantID, userID, conversationID).Scan(&shopID)
	return shopID, err
}

func conversationHistoryStale(ctx context.Context, db *pgxpool.Pool, tenantID, conversationID int64, revision string) (bool, error) {
	var stale bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM turn t JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
        WHERE t.tenant_id=$1 AND t.conversation_id=$2 AND e.permission_revision IS DISTINCT FROM $3)`,
		tenantID, conversationID, revision).Scan(&stale)
	return stale, err
}

func parentTurnIDByRequestID(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64, requestID string) (int64, error) {
	var parentID int64
	err := tx.QueryRow(ctx, `SELECT id FROM turn WHERE tenant_id=$1 AND conversation_id=$2 AND request_id=$3`,
		tenantID, conversationID, requestID).Scan(&parentID)
	return parentID, err
}

func parentTurnStatus(ctx context.Context, tx pgx.Tx, tenantID, conversationID, parentID int64) (string, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM turn WHERE tenant_id=$1 AND conversation_id=$2 AND id=$3`,
		tenantID, conversationID, parentID).Scan(&status)
	return status, err
}

func parentTurnExecutionID(ctx context.Context, tx pgx.Tx, tenantID, parentID int64) (int64, error) {
	var executionID int64
	err := tx.QueryRow(ctx, `SELECT e.id FROM conversation_execution e
		WHERE e.tenant_id=$1 AND e.turn_id=$2 ORDER BY e.execution_no DESC LIMIT 1`,
		tenantID, parentID).Scan(&executionID)
	return executionID, err
}

type turnIdempotencyRecord struct {
	payloadHash string
	requestID   string
	status      string
	turnID      int64
	executionNo int
}

func lookupTurnIdempotency(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64, key string) (turnIdempotencyRecord, bool, error) {
	var record turnIdempotencyRecord
	err := tx.QueryRow(ctx, `SELECT i.payload_hash,e.request_id,e.status,t.id,e.execution_no FROM turn_idempotency i
		JOIN turn t ON t.tenant_id=i.tenant_id AND t.id=i.turn_id
		JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
			AND ((i.execution_id IS NOT NULL AND e.id=i.execution_id)
				OR (i.execution_id IS NULL AND e.execution_no=1))
		WHERE i.tenant_id=$1 AND i.conversation_id=$2 AND i.idempotency_key=$3`,
		tenantID, conversationID, key).Scan(&record.payloadHash, &record.requestID, &record.status,
		&record.turnID, &record.executionNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return turnIdempotencyRecord{}, false, nil
	}
	if err != nil {
		return turnIdempotencyRecord{}, false, err
	}
	return record, true, nil
}

func activeTurnExists(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64) (bool, error) {
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM turn WHERE tenant_id=$1
		AND conversation_id=$2 AND status IN ('PENDING','EXECUTING','CANCEL_REQUESTED'))`,
		tenantID, conversationID).Scan(&active)
	return active, err
}

// insertTurnAggregate creates the turn, execution, idempotency and first user
// message in the caller's transaction; any error leaves the transaction to roll
// back as a unit.
func insertTurnAggregate(ctx context.Context, tx pgx.Tx, tenantID, conversationID int64,
	requestID, query, key, payloadHash, permissionRevision string, deadline time.Time,
	mock bool, parentTurnID *int64) (turnID, executionID int64, err error) {
	var turnNo int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(turn_no),0)+1 FROM turn
		WHERE tenant_id=$1 AND conversation_id=$2`, tenantID, conversationID).Scan(&turnNo); err != nil {
		return 0, 0, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status,parent_turn_id)
		VALUES($1,$2,$3,$4,$5,'EXECUTING',$6) RETURNING id`, tenantID,
		conversationID, turnNo, requestID, query, parentTurnID).Scan(&turnID); err != nil {
		return 0, 0, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,deadline_at,permission_revision)
		VALUES($1,$2,1,$3,'EXECUTING',$6,$4,$5) RETURNING id`,
		tenantID, turnID, requestID, deadline, permissionRevision, mock).Scan(&executionID); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO turn_idempotency(tenant_id,conversation_id,idempotency_key,payload_hash,turn_id,execution_id)
		VALUES($1,$2,$3,$4,$5,$6)`, tenantID, conversationID, key, payloadHash, turnID, executionID); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO message(tenant_id,turn_id,role,content) VALUES($1,$2,'user',$3)`,
		tenantID, turnID, query); err != nil {
		return 0, 0, err
	}
	return turnID, executionID, nil
}

func updateConversationTitle(ctx context.Context, db *pgxpool.Pool, tenantID, conversationID int64, title string) error {
	_, err := db.Exec(ctx, `UPDATE conversation SET title=CASE WHEN title IS NULL OR title IN ('','新会话') THEN $3 ELSE title END
		WHERE tenant_id=$1 AND id=$2`, tenantID, conversationID, title)
	return err
}
