package admin

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Review and version domain. Every version state transition (publish, rollback,
// revoke, review) is dispatched as a control command, so the statements here are
// reads plus the two suggestion writes; there is no inline "check status, change
// status, audit" chain to keep together in this package.

// ReviewQueueRow is one row of the review queue.
type ReviewQueueRow struct {
	VersionID          int64
	DocumentID         string
	Title              string
	ShopID             string
	SourceHash         string
	Status             string
	DisclosureClass    string
	ExternalAllowed    bool
	ReviewStatus       string
	ReviewedHash       *string
	FencingEpoch       int64
	ChunkCount         *int32
	PipelineVersion    string
	Quality            []byte
	CreatedAt          time.Time
	JobID              *int64
	Stage              string
	SuggestionID       int64
	SuggestionDecision string
	SuggestionReasons  []byte
	SuggestionEvidence []string
	SuggestionCreated  time.Time
}

// ReviewQueue lists versions awaiting review within the caller's shops.
func (s *Store) ReviewQueue(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string, suggestion string) ([]ReviewQueueRow, error) {
	rows, err := db.Query(ctx, `SELECT v.id,d.logical_key,d.title,s.shop_id,v.source_hash,v.status,v.Disclosure_class,
		v.external_allowed,v.review_status,v.reviewed_hash,v.fencing_epoch,v.chunk_count,v.pipeline_version,v.quality_json,v.created_at,
		j.id,COALESCE(j.stage,''),COALESCE(sug.id,0),COALESCE(sug.decision,''),COALESCE(sug.reasons,'[]'::jsonb),COALESCE(sug.evidence_chunk_ids,'{}'::text[]),COALESCE(sug.created_at,to_timestamp(0))
		FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		LEFT JOIN ingest_job_item i ON i.tenant_id=v.tenant_id AND i.document_version_id=v.id
		LEFT JOIN ingest_job j ON j.tenant_id=i.tenant_id AND j.id=i.job_id
		LEFT JOIN LATERAL (SELECT id,decision,reasons,evidence_chunk_ids,created_at FROM review_suggestion rs
			WHERE rs.tenant_id=v.tenant_id AND rs.version_id=v.id AND rs.content_hash=v.source_hash
			ORDER BY rs.created_at DESC,rs.id DESC LIMIT 1) sug ON true
		WHERE v.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		AND ($3='' OR sug.decision=$3)
		ORDER BY v.created_at DESC LIMIT 300`, tenantID, shops, suggestion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ReviewQueueRow, 0)
	for rows.Next() {
		var row ReviewQueueRow
		if err := rows.Scan(&row.VersionID, &row.DocumentID, &row.Title, &row.ShopID, &row.SourceHash, &row.Status,
			&row.DisclosureClass, &row.ExternalAllowed, &row.ReviewStatus, &row.ReviewedHash, &row.FencingEpoch,
			&row.ChunkCount, &row.PipelineVersion, &row.Quality, &row.CreatedAt, &row.JobID, &row.Stage,
			&row.SuggestionID, &row.SuggestionDecision, &row.SuggestionReasons, &row.SuggestionEvidence,
			&row.SuggestionCreated); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// ReviewDetail loads the jsonb payload of one version inside the caller's shops.
func (s *Store) ReviewDetail(ctx context.Context, db *pgxpool.Pool, tenantID, versionID int64, shops []string) ([]byte, error) {
	var data []byte
	err := db.QueryRow(ctx, `SELECT jsonb_build_object('version_id',v.id::text,'document_id',d.logical_key,'title',d.title,
		'shop_id',s.shop_id,'source_hash',v.source_hash,'status',v.status,'disclosure_class',v.Disclosure_class,
		'external_allowed',v.external_allowed,'review_status',v.review_status,'reviewed_hash',v.reviewed_hash,
		'fencing_epoch',v.fencing_epoch,'chunk_count',v.chunk_count,'quality',v.quality_json,
		'content',COALESCE((SELECT jsonb_agg(jsonb_build_object('chunk_index',c.chunk_index,'content',c.content,
			'chunk_id','db-chunk-'||c.id::text,'heading_path',c.heading_path,'content_type',c.content_type,
			'split_reason',c.split_reason,'source_position',c.meta_json->'source_position',
			'char_start',c.char_start,'char_end',c.char_end,'warning',c.meta_json->'warning',
			'table_body',c.meta_json->'table_body','table_caption',c.meta_json->'table_caption') ORDER BY c.chunk_index)
			FROM chunk c WHERE c.tenant_id=v.tenant_id AND c.version_id=v.id),'[]'::jsonb),'path',sf.relative_path,'media_type',sf.media_type)
		FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		LEFT JOIN source_file sf ON sf.tenant_id=v.tenant_id AND sf.source_manifest_id=v.source_manifest_id AND sf.document_id=d.logical_key
		WHERE v.tenant_id=$1 AND v.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`,
		tenantID, versionID, shops).Scan(&data)
	return data, err
}

// LatestSuggestion returns the newest suggestion payload for a version hash.
func (s *Store) LatestSuggestion(ctx context.Context, db *pgxpool.Pool, tenantID, versionID int64, contentHash string) ([]byte, error) {
	var payload []byte
	err := db.QueryRow(ctx, `SELECT jsonb_build_object('decision',decision,'reasons',reasons,
		'evidence_chunk_ids',evidence_chunk_ids,'created_at',created_at)
		FROM review_suggestion WHERE tenant_id=$1 AND version_id=$2 AND content_hash=$3
		ORDER BY created_at DESC,id DESC LIMIT 1`, tenantID, versionID, contentHash).Scan(&payload)
	return payload, err
}

// VersionRow is one row of the version history.
type VersionRow struct {
	VersionID       int64
	DocumentID      string
	Title           string
	ShopID          string
	Status          string
	ReviewStatus    string
	SourceHash      string
	ReviewedHash    *string
	DisclosureClass string
	ExternalAllowed bool
	FencingEpoch    int64
	ChunkCount      *int32
	CreatedAt       time.Time
	ActivatedAt     *time.Time
}

// Versions lists the version history within the caller's shops.
func (s *Store) Versions(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string) ([]VersionRow, error) {
	rows, err := db.Query(ctx, `SELECT v.id,d.logical_key,d.title,s.shop_id,v.status,v.review_status,v.source_hash,v.reviewed_hash,
		v.Disclosure_class,v.external_allowed,v.fencing_epoch,v.chunk_count,v.created_at,v.activated_at
		FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		ORDER BY v.created_at DESC LIMIT 500`, tenantID, shops)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]VersionRow, 0)
	for rows.Next() {
		var row VersionRow
		if err := rows.Scan(&row.VersionID, &row.DocumentID, &row.Title, &row.ShopID, &row.Status, &row.ReviewStatus,
			&row.SourceHash, &row.ReviewedHash, &row.DisclosureClass, &row.ExternalAllowed, &row.FencingEpoch,
			&row.ChunkCount, &row.CreatedAt, &row.ActivatedAt); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// VersionScopeRow is the identity a command needs before it may act.
type VersionScopeRow struct {
	ShopID       string
	DocumentID   string
	SourceHash   string
	FencingEpoch int64
	Status       string
	ReviewStatus string
}

// VersionScope resolves one version for a command, limited to the caller's shops.
func (s *Store) VersionScope(ctx context.Context, db *pgxpool.Pool, tenantID, versionID int64, shops []string) (VersionScopeRow, error) {
	var row VersionScopeRow
	err := db.QueryRow(ctx, `SELECT s.shop_id,d.logical_key,v.source_hash,v.fencing_epoch,v.status,v.review_status FROM document_version v
		JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND v.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`,
		tenantID, versionID, shops).Scan(&row.ShopID, &row.DocumentID, &row.SourceHash, &row.FencingEpoch,
		&row.Status, &row.ReviewStatus)
	return row, err
}

// ReviewMaterialRow carries what a suggestion needs about one version.
type ReviewMaterialRow struct {
	SourceHash    string
	Disclosure    string
	VersionStatus string
	ReviewStatus  string
	Meta          []byte
	ShopID        string
}

// ReviewMaterial loads the version attributes used to build a suggestion.
func (s *Store) ReviewMaterial(ctx context.Context, db *pgxpool.Pool, tenantID, versionID int64, shops []string) (ReviewMaterialRow, error) {
	var row ReviewMaterialRow
	err := db.QueryRow(ctx, `SELECT v.source_hash,v.disclosure_class,v.status,v.review_status,
		d.meta_json,s.shop_id FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND v.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`,
		tenantID, versionID, shops).Scan(&row.SourceHash, &row.Disclosure, &row.VersionStatus, &row.ReviewStatus,
		&row.Meta, &row.ShopID)
	return row, err
}

// ChunkRow is one chunk of a version.
type ChunkRow struct {
	ChunkID       string
	ChunkHash     string
	EffectiveFrom *time.Time
	EffectiveTo   *time.Time
}

// VersionChunks lists the chunks of one version in index order.
func (s *Store) VersionChunks(ctx context.Context, db *pgxpool.Pool, tenantID, versionID int64) ([]ChunkRow, error) {
	rows, err := db.Query(ctx, `SELECT 'db-chunk-'||id,chunk_hash,effective_from,effective_to FROM chunk WHERE tenant_id=$1 AND version_id=$2 ORDER BY chunk_index`,
		tenantID, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ChunkRow, 0)
	for rows.Next() {
		var row ChunkRow
		if err := rows.Scan(&row.ChunkID, &row.ChunkHash, &row.EffectiveFrom, &row.EffectiveTo); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// SuggestionRecord is one review_suggestion row.
type SuggestionRecord struct {
	TenantID       int64
	VersionID      int64
	ContentHash    string
	BusinessDate   string
	RulesVersion   string
	Decision       string
	Reasons        []byte
	EvidenceChunks []string
	Attestation    string
	InputHash      string
}

// InsertSuggestion stores a suggestion, or leaves the existing row in place. The
// caller falls back to SuggestionByHash when nothing is returned.
func (s *Store) InsertSuggestion(ctx context.Context, db *pgxpool.Pool, record SuggestionRecord) (int64, time.Time, error) {
	var suggestionID int64
	var created time.Time
	err := db.QueryRow(ctx, `INSERT INTO review_suggestion(tenant_id,version_id,content_hash,business_date,
		agent_rules_version,decision,reasons,evidence_chunk_ids,agent_model_id,agent_attestation_sha256,agent_input_sha256)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'local-rules-v1',$9,$10)
		ON CONFLICT (tenant_id,version_id,content_hash,agent_rules_version) DO NOTHING
		RETURNING id,created_at`, record.TenantID, record.VersionID, record.ContentHash, record.BusinessDate,
		record.RulesVersion, record.Decision, record.Reasons, record.EvidenceChunks, record.Attestation,
		record.InputHash).Scan(&suggestionID, &created)
	return suggestionID, created, err
}

// SuggestionByHash returns the existing suggestion for the same inputs.
func (s *Store) SuggestionByHash(ctx context.Context, db *pgxpool.Pool, tenantID, versionID int64, contentHash, rulesVersion string) (int64, time.Time, error) {
	var suggestionID int64
	var created time.Time
	err := db.QueryRow(ctx, `SELECT id,created_at FROM review_suggestion WHERE tenant_id=$1 AND version_id=$2
			AND content_hash=$3 AND agent_rules_version=$4`, tenantID, versionID, contentHash, rulesVersion).
		Scan(&suggestionID, &created)
	return suggestionID, created, err
}

// BatchTarget is the state a batch approval checks before dispatching.
type BatchTarget struct {
	ContentHash   string
	Decision      string
	SourceHash    string
	VersionStatus string
	ReviewStatus  string
	Disclosure    string
	FencingEpoch  int64
	ShopID        string
}

// BatchSuggestionTarget loads one suggestion together with its version.
func (s *Store) BatchSuggestionTarget(ctx context.Context, db *pgxpool.Pool, tenantID, suggestionID, versionID int64, shops []string) (BatchTarget, error) {
	var target BatchTarget
	err := db.QueryRow(ctx, `SELECT rs.content_hash,rs.decision,v.source_hash,v.status,v.review_status,v.disclosure_class,v.fencing_epoch,s.shop_id
			FROM review_suggestion rs JOIN document_version v ON v.tenant_id=rs.tenant_id AND v.id=rs.version_id
			JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
			JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
			WHERE rs.tenant_id=$1 AND rs.id=$2 AND rs.version_id=$3 AND s.shop_id=ANY($4::text[])`,
		tenantID, suggestionID, versionID, shops).Scan(&target.ContentHash, &target.Decision, &target.SourceHash,
		&target.VersionStatus, &target.ReviewStatus, &target.Disclosure, &target.FencingEpoch, &target.ShopID)
	return target, err
}

// RecordBatchAudit writes the batch approval audit row.
func (s *Store) RecordBatchAudit(ctx context.Context, db *pgxpool.Pool, tenantID, actorID int64, reason, result string, items []byte) error {
	_, err := db.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
		VALUES($1,$2,'review_suggestion.batch_approve','review_suggestion',$3,$4,$5,jsonb_build_object('items',$6::jsonb))`,
		tenantID, actorID, "batch", reason, result, items)
	return err
}
