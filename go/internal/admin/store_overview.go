package admin

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Read helpers for the admin views. Each one keeps the statement order of the
// handler it replaced; they share a pool because every query is an independent
// single-statement read (no cross-query atomicity was required before either).

// WorkerStatus is the aggregated ingestion worker heartbeat view.
type WorkerStatus struct {
	Rows            int64
	ActiveWorkers   int64
	HeartbeatAt     *time.Time
	DegradedWorkers int64
	LastErrorCode   *string
}

// IngestWorkerStatus reads the worker heartbeat aggregate.
func (s *Store) IngestWorkerStatus(ctx context.Context, db *pgxpool.Pool) (WorkerStatus, error) {
	var status WorkerStatus
	err := db.QueryRow(ctx, `SELECT count(*),
		count(*) FILTER (WHERE heartbeat_at>=now()-interval '30 seconds' AND status='working'),
		max(heartbeat_at),
		count(*) FILTER (WHERE heartbeat_at>=now()-interval '30 seconds' AND status='degraded'),
		max(last_error_code) FILTER (WHERE heartbeat_at>=now()-interval '30 seconds' AND status='degraded')
		FROM ingest_worker_heartbeat`).Scan(&status.Rows, &status.ActiveWorkers, &status.HeartbeatAt,
		&status.DegradedWorkers, &status.LastErrorCode)
	return status, err
}

// MigrationState returns the current schema version and the unverified count.
func (s *Store) MigrationState(ctx context.Context, db *pgxpool.Pool) (string, int64, error) {
	var version string
	if err := db.QueryRow(ctx, `SELECT COALESCE(max(version),'none') FROM schema_migration`).Scan(&version); err != nil {
		return "", 0, err
	}
	var legacy int64
	err := db.QueryRow(ctx, `SELECT count(*) FROM schema_migration WHERE checksum_sha256 IS NULL`).Scan(&legacy)
	return version, legacy, err
}

// IngestJobCounts summarises ingestion jobs for the caller's shops.
func (s *Store) IngestJobCounts(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string) (total, pending, failed int64, err error) {
	err = db.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE j.status IN ('pending','running','awaiting_review')),
		count(*) FILTER (WHERE j.status='failed') FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		WHERE j.tenant_id=$1 AND s.shop_id=ANY($2::text[])`, tenantID, shops).Scan(&total, &pending, &failed)
	return total, pending, failed, err
}

// RecentExecutionErrors summarises the last fifteen minutes of executions.
func (s *Store) RecentExecutionErrors(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string) (total, failed int64, err error) {
	err = db.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE e.status IN ('FAILED','failed')) FROM conversation_execution e
		JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE e.tenant_id=$1 AND c.shop_id=ANY($2::text[]) AND e.created_at >= now()-interval '15 minutes'`,
		tenantID, shops).Scan(&total, &failed)
	return total, failed, err
}

// VersionStatusCounts groups document versions by state for the caller's shops.
func (s *Store) VersionStatusCounts(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string) (map[string]int64, error) {
	rows, err := db.Query(ctx, `SELECT v.status,count(*) FROM document_version v
		JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		GROUP BY v.status`, tenantID, shops)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int64)
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		counts[state] = count
	}
	return counts, rows.Err()
}
