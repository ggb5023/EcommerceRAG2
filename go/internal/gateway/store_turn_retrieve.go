package gateway

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type evidenceSnapshotRow struct {
	tenantID         int64
	executionID      int64
	evidenceID       string
	documentID       string
	versionID        string
	sourceRef        string
	content          string
	disclosure       string
	customerEligible bool
	citationIndex    int32
	shopID           string
}

func insertEvidenceSnapshot(ctx context.Context, db *pgxpool.Pool, row evidenceSnapshotRow) error {
	_, err := db.Exec(ctx, `INSERT INTO evidence_snapshot(tenant_id,execution_id,evidence_id,document_id,version_id,source_ref,content,disclosure_class,customer_eligible,citation_index,shop_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(tenant_id,execution_id,evidence_id) DO NOTHING`,
		row.tenantID, row.executionID, row.evidenceID, row.documentID, row.versionID, row.sourceRef,
		row.content, row.disclosure, row.customerEligible, row.citationIndex, row.shopID)
	return err
}
