package admin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Store owns the SQL the admin package used to run inline. Every method takes the
// caller's transaction: admin writes must join the business transaction, never
// open their own.
type Store struct{}

// ErrStoreNeedsTransaction is returned when a caller forgets to pass a transaction.
var ErrStoreNeedsTransaction = errors.New("admin store requires the caller's transaction")

// adminStore is the package-wide handle; it holds no connection, callers pass the
// transaction that must own the write.
var adminStore Store

// AuditEntry is one admin_audit row.
type AuditEntry struct {
	TenantID        int64
	UserID          int64
	Action          string
	ResourceType    string
	ResourceID      string
	ResourceVersion string
	ContentHash     string
	FencingEpoch    int64
	Reason          string
	Result          string
	Metadata        map[string]any
}

// AppendAudit writes one admin_audit row inside the caller's transaction.
func (s *Store) AppendAudit(ctx context.Context, tx pgx.Tx, entry AuditEntry) error {
	if tx == nil {
		return ErrStoreNeedsTransaction
	}
	encoded, err := json.Marshal(entry.Metadata)
	if err != nil {
		return err
	}
	var versionValue, hashValue, reasonValue any
	if entry.ResourceVersion != "" {
		versionValue = entry.ResourceVersion
	}
	if entry.ContentHash != "" {
		hashValue = entry.ContentHash
	}
	if entry.Reason != "" {
		reasonValue = entry.Reason
	}
	var fenceValue any
	if entry.FencingEpoch > 0 {
		fenceValue = entry.FencingEpoch
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,resource_version,content_hash,fencing_epoch,reason,result,metadata_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb)`, entry.TenantID, entry.UserID, entry.Action, entry.ResourceType, entry.ResourceID, versionValue, hashValue, fenceValue, reasonValue, entry.Result, encoded)
	return err
}

// GrantRequest is one admin grant decision evaluated inside a transaction.
type GrantRequest struct {
	TenantID int64
	UserID   int64
	Role     string
	Shop     string
}

// IssueAdminGrant reports whether the caller may grant the role on the shop. It
// runs in the caller's transaction so the check sees uncommitted state.
func (s *Store) IssueAdminGrant(ctx context.Context, tx pgx.Tx, grant GrantRequest) (bool, error) {
	if tx == nil {
		return false, ErrStoreNeedsTransaction
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT ecr_check_admin_grant($1,$2,$3,$4)`,
		grant.TenantID, grant.UserID, grant.Role, grant.Shop).Scan(&allowed)
	if err != nil {
		return false, err
	}
	return allowed, nil
}
