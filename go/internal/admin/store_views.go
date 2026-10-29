package admin

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Read helpers for the admin listings. Single-statement reads; the handlers never
// relied on two of them being consistent, so they take the pool.

// AccountRow is one row of the account summary.
type AccountRow struct {
	ID                 int64
	ExternalID         string
	DisplayName        string
	Role               string
	ShopIDs            []string
	PermissionRevision string
	RevokedAt          *time.Time
	Grants             []byte
}

// Accounts lists account summaries visible to the caller's scope.
func (s *Store) Accounts(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string, tenantWide bool) ([]AccountRow, error) {
	rows, err := db.Query(ctx, `SELECT u.id,u.external_id,COALESCE(NULLIF(u.display_name,''),u.external_id),u.Role,COALESCE(u.shop_ids,ARRAY[]::text[]),
		u.permission_revision,u.revoked_at,COALESCE(jsonb_agg(jsonb_build_object('id',gr.id::text,'role',gr.Role,
		'scope_kind',gr.scope_kind,'shop_ids',gr.shop_ids,'status',gr.status)) FILTER (WHERE gr.id IS NOT NULL),'[]'::jsonb)
		FROM app_user u LEFT JOIN admin_role_grant gr ON gr.tenant_id=u.tenant_id AND gr.user_id=u.id
		WHERE u.tenant_id=$1 AND ($3::boolean OR COALESCE(u.shop_ids,ARRAY[]::text[]) && $2::text[])
		GROUP BY u.id ORDER BY u.id LIMIT 500`, tenantID, shops, tenantWide)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AccountRow, 0)
	for rows.Next() {
		var row AccountRow
		if err := rows.Scan(&row.ID, &row.ExternalID, &row.DisplayName, &row.Role, &row.ShopIDs,
			&row.PermissionRevision, &row.RevokedAt, &row.Grants); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// RequestRow is one row of the permission request listing.
type RequestRow struct {
	ID             int64
	RequestedBy    int64
	RequesterName  string
	Role           string
	ScopeKind      string
	ShopIDs        []string
	Reason         string
	Status         string
	ReviewedBy     *int64
	ReviewedAt     *time.Time
	DecisionReason *string
	CreatedAt      time.Time
	PayloadHash    string
}

// PermissionRequests lists permission requests within the caller's scope.
func (s *Store) PermissionRequests(ctx context.Context, db *pgxpool.Pool, tenantID int64, statusFilter string,
	shops []string, tenantWide, manage bool, userID int64) ([]RequestRow, error) {
	rows, err := db.Query(ctx, `SELECT q.id,q.requested_by,COALESCE(u.display_name,u.external_id),q.Role,q.scope_kind,
		q.shop_ids,q.reason,q.status,q.reviewed_by,q.reviewed_at,q.decision_reason,q.created_at,q.payload_hash
		FROM admin_permission_request q JOIN app_user u ON u.tenant_id=q.tenant_id AND u.id=q.requested_by
		WHERE q.tenant_id=$1 AND ($2='' OR q.status=$2)
		AND (($5::boolean AND (($4::boolean AND q.scope_kind='tenant') OR (q.scope_kind='shops' AND q.shop_ids && $3::text[])))
			OR (NOT $5::boolean AND q.requested_by=$6))
		ORDER BY q.created_at DESC LIMIT 200`, tenantID, statusFilter, shops, tenantWide, manage, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]RequestRow, 0)
	for rows.Next() {
		var row RequestRow
		if err := rows.Scan(&row.ID, &row.RequestedBy, &row.RequesterName, &row.Role, &row.ScopeKind,
			&row.ShopIDs, &row.Reason, &row.Status, &row.ReviewedBy, &row.ReviewedAt, &row.DecisionReason,
			&row.CreatedAt, &row.PayloadHash); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// AuditRow is one row of the audit listing.
type AuditRow struct {
	ID              int64
	ActorUserID     int64
	Action          string
	ResourceType    string
	ResourceID      string
	ResourceVersion *string
	ContentHash     *string
	FencingEpoch    *int64
	Reason          *string
	Result          string
	Metadata        []byte
	CreatedAt       time.Time
}

// AuditRecords lists audit rows within the caller's scope.
func (s *Store) AuditRecords(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string, tenantWide bool) ([]AuditRow, error) {
	rows, err := db.Query(ctx, `SELECT id,actor_user_id,action,resource_type,resource_id,resource_version,
		content_hash,fencing_epoch,reason,result,metadata_json,created_at FROM admin_audit
		WHERE tenant_id=$1 AND (metadata_json->>'shop_id'=ANY($2::text[])
			OR COALESCE(metadata_json->'shop_ids','[]'::jsonb) ?| $2::text[] OR $3::boolean)
		ORDER BY created_at DESC,id DESC LIMIT 200`, tenantID, shops, tenantWide)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AuditRow, 0)
	for rows.Next() {
		var row AuditRow
		if err := rows.Scan(&row.ID, &row.ActorUserID, &row.Action, &row.ResourceType, &row.ResourceID,
			&row.ResourceVersion, &row.ContentHash, &row.FencingEpoch, &row.Reason, &row.Result,
			&row.Metadata, &row.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// ShopRow is one row of the shop listing.
type ShopRow struct {
	ID                  string
	Name                string
	Status              string
	PublicSlug          string
	WelcomeMessage      string
	CustomerChatEnabled bool
}

// Shops lists the shops inside the caller's scope.
func (s *Store) Shops(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string) ([]ShopRow, error) {
	rows, err := db.Query(ctx, `SELECT id,name,status,COALESCE(public_slug,''),welcome_message,customer_chat_enabled FROM shop WHERE tenant_id=$1 AND id=ANY($2::text[]) ORDER BY id`,
		tenantID, shops)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ShopRow, 0)
	for rows.Next() {
		var row ShopRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Status, &row.PublicSlug, &row.WelcomeMessage,
			&row.CustomerChatEnabled); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}
