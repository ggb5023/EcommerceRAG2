package admin

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Import domain: batches, staged items and the audit rows the endpoints write.
// Statements run in the caller's transaction when the handler opened one; reads
// take the pool.

// AgentReviewEnabled reports the latest agent-review switch for the tenant.
func (s *Store) AgentReviewEnabled(ctx context.Context, db *pgxpool.Pool, tenantID int64) (bool, error) {
	var enabled bool
	err := db.QueryRow(ctx, `SELECT enabled FROM tenant_agent_review WHERE tenant_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, tenantID).Scan(&enabled)
	return enabled, err
}

// AgentReviewRow is the newest agent-review setting with its actor.
type AgentReviewRow struct {
	Enabled bool
	Policy  string
	Updated time.Time
	Actor   string
}

// LatestAgentReviewSetting loads the newest setting for the settings endpoint.
func (s *Store) LatestAgentReviewSetting(ctx context.Context, db *pgxpool.Pool, tenantID int64) (AgentReviewRow, error) {
	var row AgentReviewRow
	err := db.QueryRow(ctx, `SELECT t.enabled,t.policy_version,t.created_at,COALESCE(u.external_id,u.role)
			FROM tenant_agent_review t JOIN app_user u ON u.tenant_id=t.tenant_id AND u.id=t.actor_user_id
			WHERE t.tenant_id=$1 ORDER BY t.created_at DESC,t.id DESC LIMIT 1`, tenantID).
		Scan(&row.Enabled, &row.Policy, &row.Updated, &row.Actor)
	return row, err
}

// ExpiredStagedBatches maps each expired staged batch to its staged file paths.
func (s *Store) ExpiredStagedBatches(ctx context.Context, db *pgxpool.Pool, tenantID int64, shopID string) (map[int64][]string, error) {
	rows, err := db.Query(ctx, `SELECT b.id,i.temp_path FROM import_batch b JOIN import_item i ON i.batch_id=b.id
		WHERE b.tenant_id=$1 AND b.shop_id=$2 AND b.status='staged' AND b.expires_at < now()`, tenantID, shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batches := map[int64][]string{}
	for rows.Next() {
		var id int64
		var path string
		if rows.Scan(&id, &path) == nil {
			batches[id] = append(batches[id], path)
		}
	}
	return batches, rows.Err()
}

// MarkBatchExpiredForShop expires one staged batch of a shop.
func (s *Store) MarkBatchExpiredForShop(ctx context.Context, db *pgxpool.Pool, tenantID int64, shopID string, batchID int64) error {
	_, err := db.Exec(ctx, `UPDATE import_batch SET status='expired' WHERE tenant_id=$1 AND shop_id=$2 AND id=$3 AND status='staged'`, tenantID, shopID, batchID)
	return err
}

// ExistingSourceHashes returns which of the given hashes already exist for the shop.
func (s *Store) ExistingSourceHashes(ctx context.Context, db *pgxpool.Pool, tenantID int64, shopID string, hashes []string) map[string]bool {
	existing := map[string]bool{}
	rows, err := db.Query(ctx, `SELECT DISTINCT v.source_hash FROM document_version v
		JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND s.shop_id=$2 AND v.source_hash=ANY($3)`, tenantID, shopID, hashes)
	if err != nil {
		return existing
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		if rows.Scan(&hash) == nil {
			existing[hash] = true
		}
	}
	return existing
}

// ImportBatchRecord is one staged batch to insert.
type ImportBatchRecord struct {
	TenantID     int64
	ShopID       string
	CreatedBy    int64
	BusinessDate string
	RulesVersion string
	AgentModelID string
	ManifestYAML string
	ManifestHash string
}

// InsertImportBatch creates the batch row and returns its id.
func (s *Store) InsertImportBatch(ctx context.Context, tx pgx.Tx, record ImportBatchRecord) (int64, error) {
	if tx == nil {
		return 0, ErrStoreNeedsTransaction
	}
	var batchID int64
	err := tx.QueryRow(ctx, `INSERT INTO import_batch(tenant_id,shop_id,created_by,business_date,agent_rules_version,agent_model_id,manifest_yaml,manifest_sha256)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, record.TenantID, record.ShopID, record.CreatedBy,
		record.BusinessDate, record.RulesVersion, record.AgentModelID, record.ManifestYAML,
		record.ManifestHash).Scan(&batchID)
	if err != nil {
		return 0, err
	}
	return batchID, nil
}

// InsertImportItem stages one document of the batch.
func (s *Store) InsertImportItem(ctx context.Context, tx pgx.Tx, batchID int64, sourceHash, tempPath, disclosure string,
	effective any, precheckJSON, detailJSON []byte) error {
	if tx == nil {
		return ErrStoreNeedsTransaction
	}
	_, err := tx.Exec(ctx, `INSERT INTO import_item(batch_id,source_hash,temp_path,suggested_disclosure_class,suggested_effective_from,precheck_result,precheck_detail)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, batchID, sourceHash, tempPath, disclosure, effective, precheckJSON, detailJSON)
	return err
}

// ImportBatchScopeRow is the identity check every batch endpoint starts with.
type ImportBatchScopeRow struct {
	ShopID    string
	Status    string
	ExpiresAt time.Time
}

// ImportBatchScope loads the batch scope row.
func (s *Store) ImportBatchScope(ctx context.Context, db *pgxpool.Pool, tenantID, batchID int64) (ImportBatchScopeRow, error) {
	var row ImportBatchScopeRow
	err := db.QueryRow(ctx, `SELECT shop_id,status,expires_at FROM import_batch WHERE tenant_id=$1 AND id=$2`, tenantID, batchID).
		Scan(&row.ShopID, &row.Status, &row.ExpiresAt)
	return row, err
}

// MarkBatchExpired expires one staged batch by id.
func (s *Store) MarkBatchExpired(ctx context.Context, db *pgxpool.Pool, tenantID, batchID int64) error {
	_, err := db.Exec(ctx, `UPDATE import_batch SET status='expired' WHERE tenant_id=$1 AND id=$2 AND status='staged'`, tenantID, batchID)
	return err
}

// BatchPreviewMeta is the header of the import preview.
type BatchPreviewMeta struct {
	Status       string
	ManifestHash string
	BusinessDate time.Time
	RevokedAt    *time.Time
}

// BatchPreviewMetadata loads the preview header.
func (s *Store) BatchPreviewMetadata(ctx context.Context, db *pgxpool.Pool, tenantID, batchID int64) (BatchPreviewMeta, error) {
	var meta BatchPreviewMeta
	err := db.QueryRow(ctx, `SELECT status,manifest_sha256,business_date,revoked_at FROM import_batch WHERE tenant_id=$1 AND id=$2`, tenantID, batchID).
		Scan(&meta.Status, &meta.ManifestHash, &meta.BusinessDate, &meta.RevokedAt)
	return meta, err
}

// BatchItemRow is one staged item of a batch.
type BatchItemRow struct {
	ID            int64
	SourceHash    string
	Disclosure    string
	Precheck      []byte
	Detail        []byte
	EffectiveFrom string
	TempPath      string
}

// BatchItems lists the staged items of a batch in id order.
func (s *Store) BatchItems(ctx context.Context, db *pgxpool.Pool, batchID int64) ([]BatchItemRow, error) {
	rows, err := db.Query(ctx, `SELECT id,source_hash,suggested_disclosure_class,precheck_result,precheck_detail,
		COALESCE(suggested_effective_from::text,''),temp_path FROM import_item WHERE batch_id=$1 ORDER BY id`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]BatchItemRow, 0)
	for rows.Next() {
		var row BatchItemRow
		if err := rows.Scan(&row.ID, &row.SourceHash, &row.Disclosure, &row.Precheck, &row.Detail,
			&row.EffectiveFrom, &row.TempPath); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// BatchConfirmState loads the state and manifest used by the confirm endpoint.
func (s *Store) BatchConfirmState(ctx context.Context, db *pgxpool.Pool, tenantID, batchID int64) (string, string, error) {
	var state, manifest string
	err := db.QueryRow(ctx, `SELECT status,manifest_yaml FROM import_batch WHERE tenant_id=$1 AND id=$2`, tenantID, batchID).
		Scan(&state, &manifest)
	return state, manifest, err
}

// StagedFileRow is one staged file of a batch.
type StagedFileRow struct {
	TempPath string
	Detail   []byte
}

// BatchStagedFiles lists the staged files of a batch in id order.
func (s *Store) BatchStagedFiles(ctx context.Context, db *pgxpool.Pool, batchID int64) ([]StagedFileRow, error) {
	rows, err := db.Query(ctx, `SELECT temp_path,precheck_detail FROM import_item WHERE batch_id=$1 ORDER BY id`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]StagedFileRow, 0)
	for rows.Next() {
		var row StagedFileRow
		if err := rows.Scan(&row.TempPath, &row.Detail); err != nil {
			return nil, err
		}
		files = append(files, row)
	}
	return files, rows.Err()
}

// MarkBatchConfirmed records the queued job on a staged batch.
func (s *Store) MarkBatchConfirmed(ctx context.Context, db *pgxpool.Pool, jobID, tenantID, batchID int64) error {
	_, err := db.Exec(ctx, `UPDATE import_batch SET status='confirmed',ingest_job_id=$1 WHERE tenant_id=$2 AND id=$3 AND status='staged'`, jobID, tenantID, batchID)
	return err
}

// RecordImportConfirmAudit writes the confirm audit row (best effort at the call site).
func (s *Store) RecordImportConfirmAudit(ctx context.Context, db *pgxpool.Pool, tenantID, actorID, batchID int64,
	reason string, jobID int64, rulesVersion string) error {
	_, err := db.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
		VALUES($1,$2,'import.confirm','import_batch',$3,$4,'success',jsonb_build_object('job_id',$5::bigint,'rules_version',$6::text))`,
		tenantID, actorID, int64Text(batchID), reason, jobID, rulesVersion)
	return err
}

// BatchStatus returns the current batch state.
func (s *Store) BatchStatus(ctx context.Context, db *pgxpool.Pool, tenantID, batchID int64) (string, error) {
	var state string
	err := db.QueryRow(ctx, `SELECT status FROM import_batch WHERE tenant_id=$1 AND id=$2`, tenantID, batchID).Scan(&state)
	return state, err
}

// RevokeItemRow links a staged item to its document for revocation.
type RevokeItemRow struct {
	Detail     []byte
	SourceHash string
}

// BatchRevokeItems lists the staged items used to resolve versions.
func (s *Store) BatchRevokeItems(ctx context.Context, db *pgxpool.Pool, batchID int64) ([]RevokeItemRow, error) {
	rows, err := db.Query(ctx, `SELECT precheck_detail,source_hash FROM import_item WHERE batch_id=$1`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]RevokeItemRow, 0)
	for rows.Next() {
		var row RevokeItemRow
		if err := rows.Scan(&row.Detail, &row.SourceHash); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// RevokeVersion is the newest version matching a document key and hash.
type RevokeVersion struct {
	ID           int64
	Status       string
	ReviewStatus string
	SourceHash   string
	FencingEpoch int64
}

// VersionForRevoke resolves the version a batch revocation would act on.
func (s *Store) VersionForRevoke(ctx context.Context, db *pgxpool.Pool, tenantID int64, documentID, sourceHash string) (RevokeVersion, error) {
	var version RevokeVersion
	err := db.QueryRow(ctx, `SELECT v.id,v.status,v.review_status,v.source_hash,v.fencing_epoch FROM document_version v
				JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
				WHERE v.tenant_id=$1 AND d.logical_key=$2 AND v.source_hash=$3
				ORDER BY v.id DESC LIMIT 1`, tenantID, documentID, sourceHash).
		Scan(&version.ID, &version.Status, &version.ReviewStatus, &version.SourceHash, &version.FencingEpoch)
	return version, err
}

// BatchTempPaths lists the staged file paths of a batch.
func (s *Store) BatchTempPaths(ctx context.Context, db *pgxpool.Pool, batchID int64) []string {
	rows, err := db.Query(ctx, `SELECT temp_path FROM import_item WHERE batch_id=$1`, batchID)
	if err != nil || rows == nil {
		return nil
	}
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if rows.Scan(&path) == nil {
			paths = append(paths, path)
		}
	}
	return paths
}

// MarkBatchRevoked marks the batch revoked.
func (s *Store) MarkBatchRevoked(ctx context.Context, db *pgxpool.Pool, tenantID, batchID int64) error {
	_, err := db.Exec(ctx, `UPDATE import_batch SET status='revoked',revoked_at=now() WHERE tenant_id=$1 AND id=$2`, tenantID, batchID)
	return err
}

// RecordImportRevokeAudit writes the revoke audit row (best effort at the call site).
func (s *Store) RecordImportRevokeAudit(ctx context.Context, db *pgxpool.Pool, tenantID, actorID, batchID int64,
	reason, rulesVersion string) error {
	_, err := db.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
		VALUES($1,$2,'import.revoke','import_batch',$3,$4,'success',jsonb_build_object('rules_version',$5::text))`,
		tenantID, actorID, int64Text(batchID), reason, rulesVersion)
	return err
}

// int64Text renders a batch id for a text column.
func int64Text(value int64) string { return strconv.FormatInt(value, 10) }

// ClaimBatchForConfirm moves a staged batch into 'confirming'. The status guard is
// the claim: only one caller can win it, so two concurrent confirms cannot both
// queue a job.
func (s *Store) ClaimBatchForConfirm(ctx context.Context, db *pgxpool.Pool, tenantID int64, shop string, batchID int64) (int64, error) {
	tag, err := db.Exec(ctx, `UPDATE import_batch SET status='confirming', confirm_started_at=now()
		WHERE tenant_id=$1 AND shop_id=$2 AND id=$3 AND status='staged'`, tenantID, shop, batchID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ReleaseConfirmClaim returns a claimed batch to 'staged' so the merchant can
// retry. Submit carries an idempotency key, so the retry cannot queue a second
// job (refactor-input #14); keeping the batch failed would need a manual reset.
func (s *Store) ReleaseConfirmClaim(ctx context.Context, db *pgxpool.Pool, tenantID, actorID, batchID int64,
	cause, rulesVersion string) error {
	tag, err := db.Exec(ctx, `UPDATE import_batch SET status='staged', confirm_started_at=NULL
		WHERE tenant_id=$1 AND id=$2 AND status='confirming'`, tenantID, batchID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	_, err = db.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
		VALUES($1,$2,'import.confirm','import_batch',$3,'confirm_queue_failed','failed',jsonb_build_object('cause',$4::text,'rules_version',$5::text))`,
		tenantID, actorID, int64Text(batchID), cause, rulesVersion)
	return err
}

// SettleConfirmSuccess records the queued job. The status guard rejects a batch
// that was reclaimed by the timeout sweep while the queue call was in flight.
func (s *Store) SettleConfirmSuccess(ctx context.Context, db *pgxpool.Pool, jobID, tenantID, batchID int64) (int64, error) {
	tag, err := db.Exec(ctx, `UPDATE import_batch SET status='confirmed', ingest_job_id=$1, confirm_started_at=NULL
		WHERE tenant_id=$2 AND id=$3 AND status='confirming'`, jobID, tenantID, batchID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ReclaimTimedOutConfirms returns batches stuck in 'confirming' to 'staged'.
// Both columns move in one statement so the CHECK constraint always holds.
func (s *Store) ReclaimTimedOutConfirms(ctx context.Context, db *pgxpool.Pool, tenantID int64, actorID int64) (int64, error) {
	rows, err := db.Query(ctx, `UPDATE import_batch SET status='staged', confirm_started_at=NULL
		WHERE tenant_id=$1 AND status='confirming' AND confirm_started_at < now() - interval '10 minutes'
		RETURNING id`, tenantID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	reclaimed := make([]int64, 0)
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			reclaimed = append(reclaimed, id)
		}
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range reclaimed {
		_, _ = db.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
			VALUES($1,$2,'import.confirm','import_batch',$3,'confirm_timeout','failed',jsonb_build_object('rules_version',$4::text))`,
			tenantID, actorID, int64Text(id), importRulesVersion)
	}
	return int64(len(reclaimed)), nil
}

// Sources listing for the merchant console.
type SourceRow struct {
	ID           int64
	ShopID       string
	Type         string
	URI          *string
	CreatedAt    time.Time
	Documents    int64
	LatestImport *time.Time
}

// Sources lists the non-synthetic sources of the caller's shops.
func (s *Store) Sources(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string) ([]SourceRow, error) {
	rows, err := db.Query(ctx, `SELECT s.id,s.shop_id,s.type,CASE WHEN s.type='admin_manifest' THEN s.uri ELSE NULL END,s.created_at,
		(SELECT count(*) FROM document d WHERE d.tenant_id=s.tenant_id AND d.source_id=s.id) AS documents,
		(SELECT max(sm.created_at) FROM source_manifest sm WHERE sm.tenant_id=s.tenant_id AND sm.source_id=s.id) AS latest_import
		FROM source s WHERE s.tenant_id=$1 AND s.shop_id=ANY($2::text[])
		AND s.type<>'synthetic-acl' ORDER BY s.created_at DESC LIMIT 300`, tenantID, shops)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SourceRow, 0)
	for rows.Next() {
		var row SourceRow
		if err := rows.Scan(&row.ID, &row.ShopID, &row.Type, &row.URI, &row.CreatedAt, &row.Documents,
			&row.LatestImport); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// JobRow is one ingestion job summary.
type JobRow struct {
	ID              int64
	Stage           string
	Status          string
	ShopID          string
	ManifestHash    string
	DatasetHash     string
	FencingEpoch    int64
	CancelRequested bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Items           int64
	Pending         int64
	Running         int64
	Awaiting        int64
	Done            int64
	Failed          int64
}

// Jobs lists ingestion jobs for the caller's shops.
func (s *Store) Jobs(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string) ([]JobRow, error) {
	rows, err := db.Query(ctx, `SELECT j.id,j.stage,j.status,s.shop_id,j.manifest_sha256,j.dataset_sha256,
		j.fencing_epoch,j.cancel_requested,j.created_at,j.updated_at,count(i.id),
		count(i.id) FILTER (WHERE i.status='pending'),count(i.id) FILTER (WHERE i.status='running'),
		count(i.id) FILTER (WHERE i.status='awaiting_review'),count(i.id) FILTER (WHERE i.status='done'),
		count(i.id) FILTER (WHERE i.status='failed') FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		LEFT JOIN ingest_job_item i ON i.tenant_id=j.tenant_id AND i.job_id=j.id
		WHERE j.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		GROUP BY j.id,s.shop_id ORDER BY j.created_at DESC LIMIT 200`, tenantID, shops)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]JobRow, 0)
	for rows.Next() {
		var row JobRow
		if err := rows.Scan(&row.ID, &row.Stage, &row.Status, &row.ShopID, &row.ManifestHash, &row.DatasetHash,
			&row.FencingEpoch, &row.CancelRequested, &row.CreatedAt, &row.UpdatedAt, &row.Items, &row.Pending,
			&row.Running, &row.Awaiting, &row.Done, &row.Failed); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// JobDetailRow is one ingestion job with its lease state.
type JobDetailRow struct {
	Status          string
	Stage           string
	ShopID          string
	ManifestHash    string
	DatasetHash     *string
	FencingEpoch    int64
	CancelRequested bool
	LeaseExpiresAt  *time.Time
	ErrorCode       *string
}

// JobDetail loads one ingestion job inside the caller's shops.
func (s *Store) JobDetail(ctx context.Context, db *pgxpool.Pool, tenantID, jobID int64, shops []string) (JobDetailRow, error) {
	var row JobDetailRow
	err := db.QueryRow(ctx, `SELECT j.status,j.stage,s.shop_id,j.manifest_sha256,j.dataset_sha256,
		j.fencing_epoch,j.cancel_requested,j.lease_expires_at,j.error_code FROM ingest_job j
		JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`,
		tenantID, jobID, shops).Scan(&row.Status, &row.Stage, &row.ShopID, &row.ManifestHash, &row.DatasetHash,
		&row.FencingEpoch, &row.CancelRequested, &row.LeaseExpiresAt, &row.ErrorCode)
	return row, err
}

// JobItemRow is one item of an ingestion job.
type JobItemRow struct {
	ID                int64
	DataID            string
	PayloadHash       string
	Status            string
	Stage             string
	RetryCount        int32
	ErrorCode         *string
	CancelRequested   bool
	FencingEpoch      int64
	DocumentVersionID *string
	VersionStatus     *string
	ReviewStatus      *string
	SourceHash        *string
}

// JobItems lists the items of one ingestion job.
func (s *Store) JobItems(ctx context.Context, db *pgxpool.Pool, tenantID, jobID int64, shops []string) ([]JobItemRow, error) {
	rows, err := db.Query(ctx, `SELECT i.id,i.data_id,i.payload_hash,i.status,i.stage,i.retry_count,
		i.error_code,i.cancel_requested,i.fencing_epoch,i.document_version_id::text,v.status,v.review_status,v.source_hash
		FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		JOIN ingest_job_item i ON i.tenant_id=j.tenant_id AND i.job_id=j.id
		LEFT JOIN document_version v ON v.tenant_id=i.tenant_id AND v.id=i.document_version_id
		WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'
		ORDER BY i.id LIMIT 500`, tenantID, jobID, shops)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]JobItemRow, 0)
	for rows.Next() {
		var row JobItemRow
		if err := rows.Scan(&row.ID, &row.DataID, &row.PayloadHash, &row.Status, &row.Stage, &row.RetryCount,
			&row.ErrorCode, &row.CancelRequested, &row.FencingEpoch, &row.DocumentVersionID,
			&row.VersionStatus, &row.ReviewStatus, &row.SourceHash); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// JobExists reports whether a job is visible inside the caller's shops.
func (s *Store) JobExists(ctx context.Context, db *pgxpool.Pool, tenantID, jobID int64, shops []string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
			WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest')`,
		tenantID, jobID, shops).Scan(&exists)
	return exists, err
}
