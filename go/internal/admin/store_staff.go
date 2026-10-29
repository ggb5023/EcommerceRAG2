package admin

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Write helpers for staff provisioning. They join the caller's transaction: the
// account, its permission request and its grant must commit or roll back
// together. The account and credential rows themselves belong to localauth.

// AccountExists reports whether an external id is already taken.
func (s *Store) AccountExists(ctx context.Context, tx pgx.Tx, tenantID int64, externalID string) (bool, error) {
	if tx == nil {
		return false, ErrStoreNeedsTransaction
	}
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM app_user WHERE tenant_id=$1 AND external_id=$2`,
		tenantID, externalID).Scan(&id)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CreateStaffPermissionRequest inserts the shop-scoped request for a new staff
// account. The scope kind is fixed to 'shops' by the statement itself.
func (s *Store) CreateStaffPermissionRequest(ctx context.Context, tx pgx.Tx, tenantID, userID int64, role string, shops []string, reason, payloadHash string) (int64, error) {
	if tx == nil {
		return 0, ErrStoreNeedsTransaction
	}
	var requestID int64
	err := tx.QueryRow(ctx, `INSERT INTO admin_permission_request(tenant_id,requested_by,role,scope_kind,shop_ids,reason,payload_hash)
		VALUES($1,$2,$3,'shops',$4,$5,$6) RETURNING id`, tenantID, userID, role, shops,
		reason, payloadHash).Scan(&requestID)
	if err != nil {
		return 0, err
	}
	return requestID, nil
}

// IssueStaffGrant approves the request that was just created.
func (s *Store) IssueStaffGrant(ctx context.Context, tx pgx.Tx, tenantID, userID int64, role string, shops []string, actorID, requestID int64, reason string) (int64, error) {
	if tx == nil {
		return 0, ErrStoreNeedsTransaction
	}
	var grantID int64
	err := tx.QueryRow(ctx, `SELECT ecr_issue_admin_grant($1,$2,$3,'shops',$4,$5,$6,$7)`,
		tenantID, userID, role, shops, actorID, requestID, reason).Scan(&grantID)
	if err != nil {
		return 0, err
	}
	return grantID, nil
}
