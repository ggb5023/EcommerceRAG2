package admin

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Ops reads: command results, ingestion job lookups and usage aggregation.
// Single-statement reads, same as the handlers they replace.

// CommandOutcome is one control_command row.
type CommandOutcome struct {
	ShopID string
	Action string
}

// ControlCommandResult loads the recorded outcome of one command.
func (s *Store) ControlCommandResult(ctx context.Context, db *pgxpool.Pool, tenantID, actorID int64, commandID string) (CommandOutcome, error) {
	var outcome CommandOutcome
	err := db.QueryRow(ctx, `SELECT shop_id,action FROM control_command WHERE tenant_id=$1 AND actor_user_id=$2 AND command_id=$3`,
		tenantID, actorID, commandID).Scan(&outcome.ShopID, &outcome.Action)
	return outcome, err
}

// IngestJobSummary is the fallback view for a numeric command id.
type IngestJobSummary struct {
	Status    string
	Stage     string
	ErrorCode string
	ShopID    string
}

// IngestJobFallback loads an ingestion job state for the caller's shops.
func (s *Store) IngestJobFallback(ctx context.Context, db *pgxpool.Pool, tenantID int64, jobID string, shops []string) (IngestJobSummary, error) {
	var summary IngestJobSummary
	err := db.QueryRow(ctx, `SELECT j.status,j.stage,COALESCE(j.error_code,''),s.shop_id
				FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
				WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[])`, tenantID, jobID, shops).
		Scan(&summary.Status, &summary.Stage, &summary.ErrorCode, &summary.ShopID)
	return summary, err
}

// IngestJobShop resolves the shop that owns one ingestion job.
func (s *Store) IngestJobShop(ctx context.Context, db *pgxpool.Pool, tenantID int64, jobID string, shops []string) (string, error) {
	var shop string
	err := db.QueryRow(ctx, `SELECT s.shop_id FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[])`,
		tenantID, jobID, shops).Scan(&shop)
	return shop, err
}

// UsageSlots aggregates usage_log for the tenant, optionally from a moment on.
func (s *Store) UsageSlots(ctx context.Context, db *pgxpool.Pool, tenantID int64, since *time.Time) ([]UsageRow, error) {
	query := `SELECT slot,model,coalesce(sum(input_tokens),0),coalesce(sum(output_tokens),0),count(*),
		count(*) FILTER (WHERE error_code IS NOT NULL) FROM usage_log WHERE tenant_id=$1`
	args := []any{tenantID}
	if since != nil {
		query += " AND created_at >= $2"
		args = append(args, *since)
	}
	query += " GROUP BY slot,model ORDER BY slot,model"
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UsageRow, 0)
	for rows.Next() {
		var row UsageRow
		if err := rows.Scan(&row.Slot, &row.Model, &row.InputTokens, &row.OutputTokens, &row.Calls, &row.FailedCalls); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// UsageRow is one aggregated usage_log row.
type UsageRow struct {
	Slot         string
	Model        string
	InputTokens  int64
	OutputTokens int64
	Calls        int64
	FailedCalls  int64
}
