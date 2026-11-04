package gateway

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func insertSSEEvent(ctx context.Context, tx pgx.Tx, tenantID, executionID, sequence int64, kind string, body []byte) error {
	_, err := tx.Exec(ctx, `INSERT INTO sse_event(tenant_id,execution_id,sequence,event_type,data_json) VALUES($1,$2,$3,$4,$5::jsonb)`, tenantID, executionID, sequence, kind, string(body))
	return err
}

func insertToolCallTrace(ctx context.Context, db *pgxpool.Pool, tenantID int64, requestID, toolName, status string, degraded bool) error {
	_, err := db.Exec(ctx, `INSERT INTO tool_call_trace(tenant_id,request_id,tool_name,result_status,degraded)
		VALUES($1,$2,$3,$4,$5)`, tenantID, requestID, toolName, status, degraded)
	return err
}
