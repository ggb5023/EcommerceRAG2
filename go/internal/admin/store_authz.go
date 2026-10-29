package admin

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Read helpers. They take a pool instead of a transaction: every statement here
// is a single auto-committed read and the handlers never needed them to agree
// atomically — that is unchanged from the inline version.

// RoleGrantScope returns the strongest active grant for one role.
func (s *Store) RoleGrantScope(ctx context.Context, db *pgxpool.Pool, tenantID, userID int64, role string) (string, []string, error) {
	var scopeKind string
	var shops []string
	err := db.QueryRow(ctx, `SELECT scope_kind,shop_ids FROM admin_role_grant
			WHERE tenant_id=$1 AND user_id=$2 AND role=$3 AND status='active'
			AND EXISTS(SELECT 1 FROM app_user u JOIN tenant t ON t.id=u.tenant_id
				WHERE u.tenant_id=$1 AND u.id=$2 AND u.revoked_at IS NULL AND t.status='active')
			ORDER BY CASE scope_kind WHEN 'tenant' THEN 0 ELSE 1 END LIMIT 1`,
		tenantID, userID, role).Scan(&scopeKind, &shops)
	return scopeKind, shops, err
}

// RequestDecisionScope returns the strongest grant across the decision roles.
func (s *Store) RequestDecisionScope(ctx context.Context, db *pgxpool.Pool, tenantID, userID int64, shopID string, roles []string) (string, []string, error) {
	var scopeKind string
	var shops []string
	err := db.QueryRow(ctx, `SELECT scope_kind,shop_ids FROM admin_role_grant
		WHERE tenant_id=$1 AND user_id=$2 AND role=ANY($3::text[]) AND status='active'
		AND EXISTS(SELECT 1 FROM app_user u JOIN tenant t ON t.id=u.tenant_id
			WHERE u.tenant_id=$1 AND u.id=$2 AND u.revoked_at IS NULL AND t.status='active')
		AND (scope_kind='tenant' OR $4=ANY(shop_ids))
		ORDER BY CASE role WHEN 'access_admin' THEN 0 ELSE 1 END,
			CASE scope_kind WHEN 'tenant' THEN 0 ELSE 1 END LIMIT 1`,
		tenantID, userID, roles, shopID).Scan(&scopeKind, &shops)
	return scopeKind, shops, err
}

// ActiveRoleGrants lists the caller's active administrative roles.
func (s *Store) ActiveRoleGrants(ctx context.Context, db *pgxpool.Pool, tenantID, userID int64) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT role FROM admin_role_grant WHERE tenant_id=$1 AND user_id=$2
		AND status='active' ORDER BY role`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := make([]string, 0)
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

// RoleCatalogEntry is one row of admin_role_catalog.
type RoleCatalogEntry struct {
	RoleKey        string
	StorageRole    *string
	DisplayName    string
	CatalogVersion string
	Enabled        bool
	Requestable    bool
}

// RoleCatalog lists every catalog row.
func (s *Store) RoleCatalog(ctx context.Context, db *pgxpool.Pool) ([]RoleCatalogEntry, error) {
	rows, err := db.Query(ctx, `SELECT role_key,storage_role,display_name,catalog_version,enabled,requestable
		FROM admin_role_catalog ORDER BY role_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	catalog := make([]RoleCatalogEntry, 0)
	for rows.Next() {
		var entry RoleCatalogEntry
		if err := rows.Scan(&entry.RoleKey, &entry.StorageRole, &entry.DisplayName, &entry.CatalogVersion,
			&entry.Enabled, &entry.Requestable); err != nil {
			return nil, err
		}
		catalog = append(catalog, entry)
	}
	return catalog, rows.Err()
}

// RoleCatalogRequestable reports whether a role may be self-requested.
func (s *Store) RoleCatalogRequestable(ctx context.Context, db *pgxpool.Pool, role string) (bool, error) {
	var requestable bool
	err := db.QueryRow(ctx, `SELECT requestable FROM admin_role_catalog
		WHERE storage_role=$1 AND enabled`, role).Scan(&requestable)
	return requestable, err
}

// PendingRequestTarget is the pending request a decision would act on.
type PendingRequestTarget struct {
	RequestedBy int64
	Role        string
	ScopeKind   string
	ShopIDs     []string
}

// PendingPermissionRequest loads one pending request the actor may decide.
func (s *Store) PendingPermissionRequest(ctx context.Context, tx pgx.Tx, tenantID, requestID, actorID int64) (PendingRequestTarget, error) {
	var target PendingRequestTarget
	err := tx.QueryRow(ctx, `SELECT requested_by,role,scope_kind,shop_ids FROM admin_permission_request
		WHERE tenant_id=$1 AND id=$2 AND status='pending' AND requested_by<>$3`,
		tenantID, requestID, actorID).Scan(&target.RequestedBy, &target.Role, &target.ScopeKind, &target.ShopIDs)
	return target, err
}

// RevokeGrant revokes one administrative grant through the database function.
func (s *Store) RevokeGrant(ctx context.Context, db *pgxpool.Pool, tenantID, grantID, actorID int64, reason string) error {
	_, err := db.Exec(ctx, `SELECT ecr_revoke_admin_grant($1,$2,$3,$4)`, tenantID, grantID, actorID, reason)
	return err
}
