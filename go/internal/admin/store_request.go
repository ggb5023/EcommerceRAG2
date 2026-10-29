package admin

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PermissionRequest is one row of admin_permission_request to create.
type PermissionRequest struct {
	TenantID    int64
	RequestedBy int64
	Role        string
	ScopeKind   string
	ShopIDs     []string
	Reason      string
	PayloadHash string
}

// PermissionDecision is one decision on a pending permission request.
type PermissionDecision struct {
	TenantID   int64
	RequestID  int64
	ActorID    int64
	Decision   string
	Reason     string
	TargetUser int64
	Role       string
	ScopeKind  string
	ShopIDs    []string
}

// AgentReviewSetting is one tenant_agent_review row to append.
type AgentReviewSetting struct {
	TenantID      int64
	Enabled       bool
	PolicyVersion string
	ActorUserID   int64
}

// CreatePermissionRequest inserts one pending request and returns its id.
func (s *Store) CreatePermissionRequest(ctx context.Context, tx pgx.Tx, request PermissionRequest) (int64, error) {
	if tx == nil {
		return 0, ErrStoreNeedsTransaction
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO admin_permission_request(tenant_id,requested_by,role,scope_kind,shop_ids,reason,payload_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, request.TenantID, request.RequestedBy,
		request.Role, request.ScopeKind, request.ShopIDs, request.Reason, request.PayloadHash).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// DecidePermissionRequest records the decision in the caller's transaction.
//
// An approval issues the grant through ecr_issue_admin_grant, which changes the
// request state and writes the grant in one statement; the state change and the
// grant therefore stay in the same transaction and in the original order.
func (s *Store) DecidePermissionRequest(ctx context.Context, tx pgx.Tx, decision PermissionDecision) (int64, error) {
	if tx == nil {
		return 0, ErrStoreNeedsTransaction
	}
	var tag pgconn.CommandTag
	var err error
	if decision.Decision == "approved" {
		tag, err = tx.Exec(ctx, `SELECT ecr_issue_admin_grant($1,$2,$3,$4,$5,$6,$7,$8)`,
			decision.TenantID, decision.TargetUser, decision.Role, decision.ScopeKind,
			decision.ShopIDs, decision.ActorID, decision.RequestID, decision.Reason)
	} else {
		tag, err = tx.Exec(ctx, `SELECT ecr_decide_admin_request($1,$2,$3,$4,$5)`,
			decision.TenantID, decision.RequestID, decision.ActorID, decision.Decision, decision.Reason)
	}
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// WriteAgentReviewSetting appends one tenant_agent_review row.
func (s *Store) WriteAgentReviewSetting(ctx context.Context, tx pgx.Tx, setting AgentReviewSetting) error {
	if tx == nil {
		return ErrStoreNeedsTransaction
	}
	_, err := tx.Exec(ctx, `INSERT INTO tenant_agent_review(tenant_id,enabled,policy_version,actor_user_id)
		VALUES($1,$2,$3,$4)`, setting.TenantID, setting.Enabled, setting.PolicyVersion, setting.ActorUserID)
	return err
}
