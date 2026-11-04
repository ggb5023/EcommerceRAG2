package gateway

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func lockCancelExecution(ctx context.Context, tx pgx.Tx, tenantID int64, requestID string, userID int64, shop string) (string, int64, int64, error) {
	var state string
	var executionID, turnID int64
	err := tx.QueryRow(ctx, `SELECT e.status,e.id,e.turn_id FROM conversation_execution e
		JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE e.tenant_id=$1 AND e.request_id=$2 AND c.user_id=$3 AND c.shop_id=$4 FOR UPDATE OF e`, tenantID, requestID, userID, shop).Scan(&state, &executionID, &turnID)
	return state, executionID, turnID, err
}

func markCancelRequested(ctx context.Context, tx pgx.Tx, tenantID, executionID, turnID int64) error {
	if _, err := tx.Exec(ctx, `UPDATE conversation_execution SET cancel_requested=true,status='CANCEL_REQUESTED' WHERE tenant_id=$1 AND id=$2`, tenantID, executionID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE turn SET status='CANCEL_REQUESTED' WHERE tenant_id=$1 AND id=$2`, tenantID, turnID)
	return err
}
