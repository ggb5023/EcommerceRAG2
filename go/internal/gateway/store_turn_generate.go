package gateway

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func insertCitation(ctx context.Context, db *pgxpool.Pool, tenantID, executionID int64, citationID string, rank int32) error {
	_, err := db.Exec(ctx, `INSERT INTO citation(tenant_id,execution_id,evidence_id,citation_index)
		VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenantID, executionID, citationID, rank)
	return err
}
