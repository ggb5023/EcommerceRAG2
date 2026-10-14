package admin

import (
	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"

	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const (
	adminRoleObserver = "platform_observer"
	adminRoleAccess   = "access_admin"
	adminRoleMerchant = "merchant_admin"
	adminRoleReviewer = "knowledge_reviewer"
	maxAdminUpload    = 9 * 1024 * 1024
)

type adminScope struct {
	TenantID   int64
	UserID     int64
	Role       string
	shops      []string
	tenantWide bool
}

type adminConfirmation struct {
	Confirm              bool   `json:"confirm"`
	TenantID             string `json:"tenant_id"`
	ShopID               string `json:"shop_id"`
	DocumentID           string `json:"document_id"`
	VersionHash          string `json:"version_hash"`
	PermissionRevision   string `json:"permission_revision"`
	ExpectedFencingEpoch int64  `json:"expected_fencing_epoch"`
	Reason               string `json:"reason"`
}

func (g *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/v1/session", g.adminSession)
	mux.HandleFunc("GET /admin/v1/overview", g.adminOverview)
	mux.HandleFunc("GET /admin/v1/access/accounts", g.adminAccounts)
	mux.HandleFunc("GET /admin/v1/access/requests", g.adminListRequests)
	mux.HandleFunc("POST /admin/v1/access/requests", g.adminCreateRequest)
	mux.HandleFunc("POST /admin/v1/access/requests/{id}/decision", g.adminDecideRequest)
	mux.HandleFunc("GET /admin/v1/access/audit", g.adminAudit)
	mux.HandleFunc("POST /admin/v1/access/grants/{id}/revoke", g.adminRevokeGrant)
	mux.HandleFunc("GET /admin/v1/merchant/shops", g.adminShops)
	mux.HandleFunc("POST /admin/v1/merchant/shops/{id}/settings", g.adminShopSettings)
	mux.HandleFunc("GET /admin/v1/merchant/sources", g.adminSources)
	mux.HandleFunc("POST /admin/v1/merchant/sources/import", g.adminImportPackage)
	mux.HandleFunc("GET /admin/v1/merchant/ingestion", g.adminJobs)
	mux.HandleFunc("GET /admin/v1/merchant/ingestion/{id}", g.adminJobDetail)
	mux.HandleFunc("POST /admin/v1/merchant/ingestion/{id}/cancel", g.adminCancelIngestJob)
	mux.HandleFunc("POST /admin/v1/merchant/ingestion/{id}/retry", g.adminRetryIngestJob)
	mux.HandleFunc("GET /admin/v1/merchant/reviews", g.adminReviews)
	mux.HandleFunc("GET /admin/v1/merchant/reviews/{id}", g.adminReviewDetail)
	mux.HandleFunc("POST /admin/v1/merchant/versions/{id}/review", g.adminReviewVersion)
	mux.HandleFunc("POST /admin/v1/merchant/versions/{id}/publish", g.adminPublishVersion)
	mux.HandleFunc("POST /admin/v1/merchant/versions/{id}/rollback", g.adminRollbackVersion)
	mux.HandleFunc("POST /admin/v1/merchant/versions/{id}/revoke", g.adminRevokeVersion)
	mux.HandleFunc("GET /admin/v1/merchant/versions", g.adminVersions)
	mux.HandleFunc("GET /admin/v1/merchant/commands/{id}", g.adminCommandResult)
}

func (g *Handler) requireAdmin(w http.ResponseWriter, r *http.Request, roles ...string) (adminScope, bool) {
	if g.DB == nil {
		httpapi.WriteError(w, http.StatusForbidden, "admin_scope_denied", "administrative permission is required")
		return adminScope{}, false
	}
	if err := g.Authority.Refresh(r.Context()); err != nil {
		httpapi.WriteError(w, auth.HTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return adminScope{}, false
	}
	shopID := strings.TrimSpace(r.URL.Query().Get("shop_id"))
	explicitShop := shopID != ""
	if shopID == "" {
		shopID = g.Session.Shop
	}
	if shopID != "" && !contains(g.Session.AllowedShops, shopID) {
		httpapi.WriteError(w, http.StatusForbidden, "admin_scope_denied", "shop is outside the current identity scope")
		return adminScope{}, false
	}
	for _, role := range roles {
		var scopeKind string
		var shops []string
		err := g.DB.QueryRow(r.Context(), `SELECT scope_kind,shop_ids FROM admin_role_grant
			WHERE tenant_id=$1 AND user_id=$2 AND role=$3 AND status='active'
			AND EXISTS(SELECT 1 FROM app_user u JOIN tenant t ON t.id=u.tenant_id
				WHERE u.tenant_id=$1 AND u.id=$2 AND u.revoked_at IS NULL AND t.status='active')
			ORDER BY CASE scope_kind WHEN 'tenant' THEN 0 ELSE 1 END LIMIT 1`,
			g.Session.TenantID, g.Session.UserID, role).Scan(&scopeKind, &shops)
		if err == nil && (scopeKind == "tenant" || contains(shops, shopID)) {
			if err = g.Authority.Refresh(r.Context()); err != nil {
				httpapi.WriteError(w, auth.HTTPStatus(err), auth.FailureCode(err), "authorization failed")
				return adminScope{}, false
			}
			effectiveShops := make([]string, 0, len(g.Session.AllowedShops))
			for _, allowedShop := range g.Session.AllowedShops {
				if scopeKind == "tenant" || contains(shops, allowedShop) {
					effectiveShops = append(effectiveShops, allowedShop)
				}
			}
			if explicitShop {
				effectiveShops = []string{shopID}
			}
			return adminScope{TenantID: g.Session.TenantID, UserID: g.Session.UserID,
				Role: role, shops: effectiveShops, tenantWide: scopeKind == "tenant"}, true
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			httpapi.WriteError(w, http.StatusServiceUnavailable, "authorization_unavailable", "administrative authorization is unavailable")
			return adminScope{}, false
		}
	}
	httpapi.WriteError(w, http.StatusForbidden, "admin_scope_denied", "administrative permission is required")
	return adminScope{}, false
}

func (g *Handler) permissionRequestScope(w http.ResponseWriter, r *http.Request) (adminScope, bool, bool) {
	if err := g.Authority.Refresh(r.Context()); err != nil {
		httpapi.WriteError(w, auth.HTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return adminScope{}, false, false
	}
	shopID := strings.TrimSpace(r.URL.Query().Get("shop_id"))
	explicitShop := shopID != ""
	if shopID == "" {
		shopID = g.Session.Shop
	}
	if shopID != "" && !contains(g.Session.AllowedShops, shopID) {
		httpapi.WriteError(w, http.StatusForbidden, "admin_scope_denied", "shop is outside the current identity scope")
		return adminScope{}, false, false
	}
	var scopeKind string
	var shops []string
	err := g.DB.QueryRow(r.Context(), `SELECT scope_kind,shop_ids FROM admin_role_grant
		WHERE tenant_id=$1 AND user_id=$2 AND role=ANY($3::text[]) AND status='active'
		AND EXISTS(SELECT 1 FROM app_user u JOIN tenant t ON t.id=u.tenant_id
			WHERE u.tenant_id=$1 AND u.id=$2 AND u.revoked_at IS NULL AND t.status='active')
		AND (scope_kind='tenant' OR $4=ANY(shop_ids))
		ORDER BY CASE role WHEN 'access_admin' THEN 0 ELSE 1 END,
			CASE scope_kind WHEN 'tenant' THEN 0 ELSE 1 END LIMIT 1`,
		g.Session.TenantID, g.Session.UserID, []string{adminRoleAccess, adminRoleMerchant}, shopID).Scan(&scopeKind, &shops)
	if errors.Is(err, pgx.ErrNoRows) {
		return adminScope{TenantID: g.Session.TenantID, UserID: g.Session.UserID}, false, true
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "authorization_unavailable", "permission request scope is unavailable")
		return adminScope{}, false, false
	}
	if err = g.Authority.Refresh(r.Context()); err != nil {
		httpapi.WriteError(w, auth.HTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return adminScope{}, false, false
	}
	effectiveShops := make([]string, 0, len(g.Session.AllowedShops))
	for _, allowedShop := range g.Session.AllowedShops {
		if scopeKind == "tenant" || contains(shops, allowedShop) {
			effectiveShops = append(effectiveShops, allowedShop)
		}
	}
	if explicitShop {
		effectiveShops = []string{shopID}
	}
	return adminScope{TenantID: g.Session.TenantID, UserID: g.Session.UserID,
		shops: effectiveShops, tenantWide: scopeKind == "tenant"}, true, true
}

func (g *Handler) adminSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.requireAdmin(w, r, adminRoleObserver, adminRoleAccess, adminRoleMerchant, adminRoleReviewer); !ok {
		return
	}
	roles := make([]string, 0)
	rows, err := g.DB.Query(r.Context(), `SELECT role FROM admin_role_grant WHERE tenant_id=$1 AND user_id=$2
		AND status='active' ORDER BY role`, g.Session.TenantID, g.Session.UserID)
	if err != nil {
		httpapi.WriteError(w, 503, "authorization_unavailable", "could not load administrative session")
		return
	}
	for rows.Next() {
		var role string
		if rows.Scan(&role) != nil {
			rows.Close()
			httpapi.WriteError(w, 503, "authorization_unavailable", "could not load administrative session")
			return
		}
		roles = append(roles, role)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		httpapi.WriteError(w, 503, "authorization_unavailable", "could not load administrative session")
		return
	}
	rows.Close()
	catalog := make([]map[string]any, 0)
	catalogRows, err := g.DB.Query(r.Context(), `SELECT role_key,storage_role,display_name,catalog_version,enabled,requestable
		FROM admin_role_catalog ORDER BY role_key`)
	if err != nil {
		httpapi.WriteError(w, 503, "authorization_unavailable", "could not load administrative role catalog")
		return
	}
	for catalogRows.Next() {
		var roleKey, displayName, catalogVersion string
		var storageRole *string
		var enabled, requestable bool
		if err = catalogRows.Scan(&roleKey, &storageRole, &displayName, &catalogVersion, &enabled, &requestable); err != nil {
			catalogRows.Close()
			httpapi.WriteError(w, 503, "authorization_unavailable", "could not load administrative role catalog")
			return
		}
		catalog = append(catalog, map[string]any{
			"role_key": roleKey, "storage_role": storageRole, "display_name": displayName,
			"catalog_version": catalogVersion, "enabled": enabled, "requestable": requestable,
		})
	}
	if err = catalogRows.Err(); err != nil {
		catalogRows.Close()
		httpapi.WriteError(w, 503, "authorization_unavailable", "could not load administrative role catalog")
		return
	}
	catalogRows.Close()
	httpapi.WriteJSON(w, 200, map[string]any{"tenant_id": g.Session.Tenant, "user_id": g.Session.User,
		"runtime_role": g.Session.Role, "shop_id": g.Session.Shop, "allowed_shop_ids": g.Session.AllowedShops,
		"permission_revision": g.Authority.CurrentRevision(), "admin_roles": roles,
		"admin_role_catalog": catalog, "is_mock": httpapi.IsMockProfile(g.Profile)})
}

func (g *Handler) adminOverview(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleObserver, adminRoleAccess, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	components := map[string]any{
		"go":         map[string]any{"status": "healthy", "version": envOr("APP_VERSION", "development")},
		"postgresql": map[string]any{"status": "healthy"},
		"redis":      map[string]any{"status": "not_configured"},
		"python":     map[string]any{"status": "unknown"},
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := g.DB.Ping(ctx); err != nil {
		components["postgresql"] = map[string]any{"status": "unavailable"}
	}
	if g.AIConnection != nil {
		healthClient := grpc_health_v1.NewHealthClient(g.AIConnection)
		healthCtx, healthCancel := context.WithTimeout(ctx, 1200*time.Millisecond)
		response, err := healthClient.Check(healthCtx, &grpc_health_v1.HealthCheckRequest{})
		healthCancel()
		state := "unavailable"
		if err == nil && response.Status == grpc_health_v1.HealthCheckResponse_SERVING {
			state = "healthy"
		}
		components["python"] = map[string]any{"status": state, "role": "parser_and_rag_rpc"}
	}
	if address := strings.TrimSpace(os.Getenv("REDIS_ADDR")); address != "" {
		redisState := "unavailable"
		if pingRedis(ctx, address, os.Getenv("REDIS_USERNAME"), os.Getenv("REDIS_PASSWORD")) == nil {
			redisState = "healthy"
		}
		components["redis"] = map[string]any{"status": redisState}
	}
	var workerRows, activeWorkers, degradedWorkers int64
	var workerHeartbeat *time.Time
	var workerErrorCode *string
	err := g.DB.QueryRow(ctx, `SELECT count(*),
		count(*) FILTER (WHERE heartbeat_at>=now()-interval '30 seconds' AND status='working'),
		max(heartbeat_at),
		count(*) FILTER (WHERE heartbeat_at>=now()-interval '30 seconds' AND status='degraded'),
		max(last_error_code) FILTER (WHERE heartbeat_at>=now()-interval '30 seconds' AND status='degraded')
		FROM ingest_worker_heartbeat`).Scan(&workerRows, &activeWorkers, &workerHeartbeat,
		&degradedWorkers, &workerErrorCode)
	if err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load ingestion worker status")
		return
	}
	workerStatus := "not_configured"
	workerDetail := "no ingestion worker heartbeat has been recorded"
	if workerRows > 0 {
		workerStatus = "unavailable"
		workerDetail = "last ingestion worker heartbeat is stale"
	}
	if workerHeartbeat != nil && time.Since(*workerHeartbeat) < 30*time.Second {
		workerStatus = "healthy"
		workerDetail = fmt.Sprintf("%d active job(s)", activeWorkers)
		if degradedWorkers > 0 {
			workerStatus = "degraded"
			workerDetail = fmt.Sprintf("%d worker(s) report an error", degradedWorkers)
		}
	}
	components["worker"] = map[string]any{"status": workerStatus, "detail": workerDetail,
		"active_jobs": activeWorkers, "heartbeat_at": workerHeartbeat, "last_error_code": workerErrorCode}
	var migrationVersion string
	if err := g.DB.QueryRow(ctx, `SELECT COALESCE(max(version),'none') FROM schema_migration`).Scan(&migrationVersion); err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load migration status")
		return
	}
	var legacyMigrations int64
	if err := g.DB.QueryRow(ctx, `SELECT count(*) FROM schema_migration WHERE checksum_sha256 IS NULL`).Scan(&legacyMigrations); err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load migration integrity status")
		return
	}
	var jobs, pending, failed, total, errorsLast, executionsLast int64
	err = g.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE j.status IN ('pending','running','awaiting_review')),
		count(*) FILTER (WHERE j.status='failed') FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		WHERE j.tenant_id=$1 AND s.shop_id=ANY($2::text[])`, g.Session.TenantID, scope.shops).
		Scan(&jobs, &pending, &failed)
	if err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load ingestion status")
		return
	}
	err = g.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE e.status IN ('FAILED','failed')) FROM conversation_execution e
		JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
		WHERE e.tenant_id=$1 AND c.shop_id=ANY($2::text[]) AND e.created_at >= now()-interval '15 minutes'`,
		g.Session.TenantID, scope.shops).Scan(&total, &errorsLast)
	if err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load error rate")
		return
	}
	versionRows, err := g.DB.Query(ctx, `SELECT v.status,count(*) FROM document_version v
		JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		GROUP BY v.status`, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load version status")
		return
	}
	versionCounts := make(map[string]int64)
	var versionTotal, activeVersions int64
	for versionRows.Next() {
		var state string
		var count int64
		if versionRows.Scan(&state, &count) != nil {
			versionRows.Close()
			httpapi.WriteError(w, 503, "status_unavailable", "could not load version status")
			return
		}
		versionCounts[state] = count
		versionTotal += count
		if state == "active" {
			activeVersions = count
		}
	}
	if err = versionRows.Err(); err != nil {
		versionRows.Close()
		httpapi.WriteError(w, 503, "status_unavailable", "could not load version status")
		return
	}
	versionRows.Close()
	executionsLast = total
	errorRate := float64(0)
	if executionsLast > 0 {
		errorRate = float64(errorsLast) / float64(executionsLast)
	}
	if err = g.Authority.Refresh(ctx); err != nil {
		httpapi.WriteError(w, auth.HTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{
		"components": components,
		"migration":  map[string]any{"current_version": migrationVersion, "status": "read_only", "legacy_unverified": legacyMigrations},
		"ingestion":  map[string]any{"jobs": jobs, "awaiting_or_running": pending, "failed": failed},
		"versions":   map[string]any{"total": versionTotal, "active": activeVersions, "by_status": versionCounts},
		"version": map[string]string{"application": envOr("APP_VERSION", "development"),
			"revision": envOr("DEPLOYED_REVISION", "unknown")},
		"error_rate": map[string]any{"window_minutes": 15, "requests": executionsLast, "errors": errorsLast, "rate": errorRate},
		"scope":      map[string]any{"tenant_id": g.Session.Tenant, "shop_id": g.Session.Shop},
	})
}

func pingRedis(ctx context.Context, address, username, password string) error {
	dialer := net.Dialer{Timeout: 700 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	_ = conn.SetDeadline(time.Now().Add(700 * time.Millisecond))
	if password != "" {
		args := []string{"AUTH", password}
		if username != "" {
			args = []string{"AUTH", username, password}
		}
		if _, err = io.WriteString(conn, redisCommand(args...)); err != nil {
			return err
		}
		if _, err = reader.ReadString('\n'); err != nil {
			return err
		}
	}
	if _, err = io.WriteString(conn, redisCommand("PING")); err != nil {
		return err
	}
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "PONG") {
		return errors.New("redis health check failed")
	}
	return nil
}

func redisCommand(args ...string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&builder, "$%d\r\n%s\r\n", len(arg), arg)
	}
	return builder.String()
}

func (g *Handler) adminAccounts(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleAccess)
	if !ok {
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT u.id,u.external_id,COALESCE(NULLIF(u.display_name,''),u.external_id),u.Role,COALESCE(u.shop_ids,ARRAY[]::text[]),
		u.permission_revision,u.revoked_at,COALESCE(jsonb_agg(jsonb_build_object('id',gr.id::text,'role',gr.Role,
		'scope_kind',gr.scope_kind,'shop_ids',gr.shop_ids,'status',gr.status)) FILTER (WHERE gr.id IS NOT NULL),'[]'::jsonb)
		FROM app_user u LEFT JOIN admin_role_grant gr ON gr.tenant_id=u.tenant_id AND gr.user_id=u.id
		WHERE u.tenant_id=$1 AND ($3::boolean OR COALESCE(u.shop_ids,ARRAY[]::text[]) && $2::text[])
		GROUP BY u.id ORDER BY u.id LIMIT 500`, g.Session.TenantID, scope.shops, scope.tenantWide)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load account summaries")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var externalID, displayName, role, revision string
		var shops []string
		var grants []byte
		var revoked *time.Time
		if rows.Scan(&id, &externalID, &displayName, &role, &shops, &revision, &revoked, &grants) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load account summaries")
			return
		}
		items = append(items, map[string]any{"user_id": strconv.FormatInt(id, 10), "external_id": externalID,
			"display_name": displayName, "runtime_role": role, "shop_ids": shops,
			"permission_revision": revision, "revoked": revoked != nil, "grants": json.RawMessage(grants)})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminListRequests(w http.ResponseWriter, r *http.Request) {
	scope, canManageRequests, ok := g.permissionRequestScope(w, r)
	if !ok {
		return
	}
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	rows, err := g.DB.Query(r.Context(), `SELECT q.id,q.requested_by,COALESCE(u.display_name,u.external_id),q.Role,q.scope_kind,
		q.shop_ids,q.reason,q.status,q.reviewed_by,q.reviewed_at,q.decision_reason,q.created_at,q.payload_hash
		FROM admin_permission_request q JOIN app_user u ON u.tenant_id=q.tenant_id AND u.id=q.requested_by
		WHERE q.tenant_id=$1 AND ($2='' OR q.status=$2)
		AND (($5::boolean AND (($4::boolean AND q.scope_kind='tenant') OR (q.scope_kind='shops' AND q.shop_ids && $3::text[])))
			OR (NOT $5::boolean AND q.requested_by=$6))
		ORDER BY q.created_at DESC LIMIT 200`, g.Session.TenantID, statusFilter, scope.shops,
		scope.tenantWide, canManageRequests, g.Session.UserID)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load permission requests")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, requester int64
		var reviewer *int64
		var reviewedAt *time.Time
		var displayName, role, scopeKind, reason, state, hash string
		var decision *string
		var shops []string
		var createdAt time.Time
		if rows.Scan(&id, &requester, &displayName, &role, &scopeKind, &shops, &reason, &state,
			&reviewer, &reviewedAt, &decision, &createdAt, &hash) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load permission requests")
			return
		}
		items = append(items, map[string]any{"id": strconv.FormatInt(id, 10), "requested_by": strconv.FormatInt(requester, 10),
			"requester_name": displayName, "role": role, "scope_kind": scopeKind, "shop_ids": shops,
			"reason": reason, "status": state, "reviewed_by": reviewer, "reviewed_at": reviewedAt,
			"decision_reason": decision, "created_at": createdAt, "payload_hash": hash})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminCreateRequest(w http.ResponseWriter, r *http.Request) {
	if err := g.Authority.Refresh(r.Context()); err != nil {
		httpapi.WriteError(w, auth.HTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	var in struct {
		Role   string   `json:"role"`
		Scope  string   `json:"scope_kind"`
		Shops  []string `json:"shop_ids"`
		Reason string   `json:"reason"`
	}
	if err := httpapi.DecodeJSON(r, &in); err != nil || !validAdminRequest(in.Role, in.Scope, in.Shops, in.Reason, g.Session.AllowedShops) {
		httpapi.WriteError(w, 400, "invalid_request", "permission request is invalid")
		return
	}
	var requestable bool
	if err := g.DB.QueryRow(r.Context(), `SELECT requestable FROM admin_role_catalog
		WHERE storage_role=$1 AND enabled`, in.Role).Scan(&requestable); errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 400, "role_not_requestable", "the requested administrative role is not available")
		return
	} else if err != nil {
		httpapi.WriteError(w, 503, "role_catalog_unavailable", "administrative role catalog is unavailable")
		return
	} else if !requestable {
		httpapi.WriteError(w, 400, "role_not_requestable", "the requested administrative role cannot be self-requested")
		return
	}
	if in.Shops == nil {
		in.Shops = []string{}
	}
	canonical, _ := json.Marshal(map[string]any{"tenant_id": g.Session.TenantID, "user_id": g.Session.UserID,
		"role": in.Role, "scope_kind": in.Scope, "shop_ids": in.Shops, "reason": strings.TrimSpace(in.Reason)})
	sum := sha256.Sum256(canonical)
	tx, err := g.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		httpapi.WriteError(w, 503, "request_create_failed", "could not start permission request")
		return
	}
	defer tx.Rollback(r.Context())
	var id int64
	err = tx.QueryRow(r.Context(), `INSERT INTO admin_permission_request(tenant_id,requested_by,role,scope_kind,shop_ids,reason,payload_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, g.Session.TenantID, g.Session.UserID,
		in.Role, in.Scope, in.Shops, strings.TrimSpace(in.Reason), hex.EncodeToString(sum[:])).Scan(&id)
	if err != nil {
		httpapi.WriteError(w, 503, "request_create_failed", "could not create permission request")
		return
	}
	if err = auditAdmin(r.Context(), tx, g.Session.TenantID, g.Session.UserID, "permission.request.create",
		"admin_permission_request", strconv.FormatInt(id, 10), "", "", 0, strings.TrimSpace(in.Reason), "success",
		map[string]any{"role": in.Role, "scope_kind": in.Scope, "shop_ids": in.Shops}); err != nil {
		httpapi.WriteError(w, 503, "request_create_failed", "could not record permission request")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		httpapi.WriteError(w, 503, "request_create_failed", "could not commit permission request")
		return
	}
	httpapi.WriteJSON(w, 201, map[string]any{"id": strconv.FormatInt(id, 10), "status": "pending"})
}

func (g *Handler) adminDecideRequest(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleAccess, adminRoleMerchant)
	if !ok {
		return
	}
	var in struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
		Confirm  bool   `json:"confirm"`
	}
	if err := httpapi.DecodeJSON(r, &in); err != nil || !in.Confirm || !contains([]string{"approved", "rejected", "needs_revision"}, in.Decision) || len(strings.TrimSpace(in.Reason)) < 10 {
		httpapi.WriteError(w, 400, "invalid_decision", "decision and reason are required")
		return
	}
	requestID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, 404, "not_found", "permission request not found")
		return
	}
	tx, err := g.DB.BeginTx(r.Context(), pgx.TxOptions{})
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "decision could not be started")
		return
	}
	defer tx.Rollback(r.Context())
	var targetUser int64
	var role, scopeKind string
	var shops []string
	err = tx.QueryRow(r.Context(), `SELECT requested_by,role,scope_kind,shop_ids FROM admin_permission_request
		WHERE tenant_id=$1 AND id=$2 AND status='pending' AND requested_by<>$3`,
		g.Session.TenantID, requestID, g.Session.UserID).Scan(&targetUser, &role, &scopeKind, &shops)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 409, "request_conflict", "request is unavailable or already decided")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "permission request could not be loaded")
		return
	}
	if (scopeKind == "tenant" && !scope.tenantWide) || (scopeKind == "shops" && !allContained(scope.shops, shops)) {
		httpapi.WriteError(w, 403, "admin_scope_denied", "request scope is outside the current administrative grant")
		return
	}
	if in.Decision == "approved" {
		if _, err = tx.Exec(r.Context(), `SELECT ecr_issue_admin_grant($1,$2,$3,$4,$5,$6,$7,$8)`,
			g.Session.TenantID, targetUser, role, scopeKind, shops, g.Session.UserID, requestID, strings.TrimSpace(in.Reason)); err != nil {
			httpapi.WriteError(w, databaseDecisionStatus(err), "request_conflict", "permission grant could not be issued")
			return
		}
	} else {
		_, err = tx.Exec(r.Context(), `SELECT ecr_decide_admin_request($1,$2,$3,$4,$5)`,
			g.Session.TenantID, requestID, g.Session.UserID, in.Decision, strings.TrimSpace(in.Reason))
	}
	if err != nil {
		httpapi.WriteError(w, 503, "decision_failed", "permission decision could not be recorded")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		httpapi.WriteError(w, 503, "decision_failed", "permission decision could not be committed")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"id": strconv.FormatInt(requestID, 10), "status": in.Decision,
		"audit_result": "success"})
}

func (g *Handler) adminAudit(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleAccess, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT id,actor_user_id,action,resource_type,resource_id,resource_version,
		content_hash,fencing_epoch,reason,result,metadata_json,created_at FROM admin_audit
		WHERE tenant_id=$1 AND (metadata_json->>'shop_id'=ANY($2::text[])
			OR COALESCE(metadata_json->'shop_ids','[]'::jsonb) ?| $2::text[] OR $3::boolean)
		ORDER BY created_at DESC,id DESC LIMIT 200`, g.Session.TenantID, scope.shops, scope.tenantWide)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load audit records")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, actor int64
		var action, kind, resourceID, result string
		var version, hash, reason *string
		var fence *int64
		var metadata []byte
		var created time.Time
		if rows.Scan(&id, &actor, &action, &kind, &resourceID, &version, &hash, &fence, &reason, &result, &metadata, &created) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load audit records")
			return
		}
		items = append(items, map[string]any{"id": strconv.FormatInt(id, 10), "actor_user_id": strconv.FormatInt(actor, 10),
			"action": action, "resource_type": kind, "resource_id": resourceID, "resource_version": version,
			"content_hash": hash, "fencing_epoch": fence, "reason": reason, "result": result,
			"metadata": json.RawMessage(metadata), "created_at": created})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminRevokeGrant(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.requireAdmin(w, r, adminRoleAccess); !ok {
		return
	}
	var in struct {
		Reason  string `json:"reason"`
		Confirm bool   `json:"confirm"`
	}
	if err := httpapi.DecodeJSON(r, &in); err != nil || !in.Confirm || len(strings.TrimSpace(in.Reason)) < 10 {
		httpapi.WriteError(w, 400, "invalid_request", "revocation reason is required")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, 404, "not_found", "grant not found")
		return
	}
	_, err = g.DB.Exec(r.Context(), `SELECT ecr_revoke_admin_grant($1,$2,$3,$4)`, g.Session.TenantID, id, g.Session.UserID, strings.TrimSpace(in.Reason))
	if err != nil {
		httpapi.WriteError(w, databaseDecisionStatus(err), "grant_revoke_failed", "grant could not be revoked")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"id": strconv.FormatInt(id, 10), "status": "revoked", "audit_result": "success"})
}

func (g *Handler) adminShops(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleAccess, adminRoleObserver)
	if !ok {
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT id,name,status,COALESCE(public_slug,''),welcome_message,customer_chat_enabled FROM shop WHERE tenant_id=$1 AND id=ANY($2::text[]) ORDER BY id`,
		g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load shops")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, state, slug, welcome string
		var enabled bool
		if rows.Scan(&id, &name, &state, &slug, &welcome, &enabled) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load shops")
			return
		}
		items = append(items, map[string]any{"shop_id": id, "name": name, "status": state, "public_slug": slug, "welcome_message": welcome, "customer_chat_enabled": enabled,
			"customer_chat_url": customerChatURL(g.CustomerOrigin, slug, enabled && state == "active")})
	}
	if rows.Err() != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load shops")
		return
	}
	// Reuse customer authorization; a published internal or ACL-blocked document
	// must never be advertised as available for a customer trial.
	rows.Close()
	for _, item := range items {
		documents, err := auth.CustomerDocuments(r.Context(), g.DB, g.Session.TenantID, item["shop_id"].(string))
		if err != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load customer knowledge availability")
			return
		}
		item["customer_published_knowledge_available"] = len(documents) > 0
	}
	if err := g.Authority.Refresh(r.Context()); err != nil {
		httpapi.WriteError(w, auth.HTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminSources(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleObserver)
	if !ok {
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT s.id,s.shop_id,s.type,CASE WHEN s.type='admin_manifest' THEN s.uri ELSE NULL END,s.created_at,
		(SELECT count(*) FROM document d WHERE d.tenant_id=s.tenant_id AND d.source_id=s.id) AS documents,
		(SELECT max(sm.created_at) FROM source_manifest sm WHERE sm.tenant_id=s.tenant_id AND sm.source_id=s.id) AS latest_import
		FROM source s WHERE s.tenant_id=$1 AND s.shop_id=ANY($2::text[])
		AND s.type<>'synthetic-acl' ORDER BY s.created_at DESC LIMIT 300`, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load sources")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, docs int64
		var shop, kind string
		var uri *string
		var created time.Time
		var latest *time.Time
		if rows.Scan(&id, &shop, &kind, &uri, &created, &docs, &latest) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load sources")
			return
		}
		items = append(items, map[string]any{"source_id": strconv.FormatInt(id, 10), "shop_id": shop,
			"type": kind, "uri": uri, "documents": docs, "created_at": created, "latest_import": latest})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminImportPackage(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminUpload)
	if err := r.ParseMultipartForm(maxAdminUpload); err != nil {
		httpapi.WriteError(w, 400, "upload_too_large", "manifest package is invalid or too large")
		return
	}
	shopID := strings.TrimSpace(r.FormValue("shop_id"))
	if !contains(scope.shops, shopID) {
		httpapi.WriteError(w, 403, "admin_scope_denied", "shop is outside the current identity scope")
		return
	}
	manifest := r.FormValue("manifest")
	if strings.TrimSpace(manifest) == "" || len(manifest) > 256*1024 {
		httpapi.WriteError(w, 400, "invalid_manifest", "manifest is required and must be under 256 KiB")
		return
	}
	paths := r.MultipartForm.Value["paths"]
	fileHeaders := r.MultipartForm.File["files"]
	if len(fileHeaders) == 0 || len(fileHeaders) != len(paths) || len(fileHeaders) > 100 {
		httpapi.WriteError(w, 400, "invalid_package", "manifest files do not match package paths")
		return
	}
	files := make([]*ragv1.PackageFile, 0, len(fileHeaders))
	for index, header := range fileHeaders {
		if header.Size > 8*1024*1024 {
			httpapi.WriteError(w, 413, "file_too_large", "a manifest file exceeds 8 MiB")
			return
		}
		file, err := header.Open()
		if err != nil {
			httpapi.WriteError(w, 400, "invalid_package", "an uploaded file could not be read")
			return
		}
		content, readErr := io.ReadAll(io.LimitReader(file, 8*1024*1024+1))
		_ = file.Close()
		if readErr != nil || len(content) > 8*1024*1024 {
			httpapi.WriteError(w, 413, "file_too_large", "a manifest file exceeds 8 MiB")
			return
		}
		files = append(files, &ragv1.PackageFile{Path: paths[index], Content: content})
	}
	queued, err := g.queuePackage(r.Context(), manifest, files, g.Session.TenantID, g.Session.UserID, shopID)
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			httpapi.WriteError(w, 400, "invalid_package", "manifest package failed validation")
			return
		}
		if status.Code(err) == codes.PermissionDenied {
			httpapi.WriteError(w, 403, "admin_scope_denied", "submitter authorization changed")
			return
		}
		if status.Code(err) == codes.AlreadyExists {
			httpapi.WriteError(w, 409, "version_conflict", "idempotency key was reused with a different package")
			return
		}
		var conflict *adminConflictError
		if errors.As(err, &conflict) {
			httpapi.WriteError(w, 409, "version_conflict", conflict.Error())
			return
		}
		httpapi.WriteError(w, 503, "ingestion_queue_failed", "package could not be queued")
		return
	}
	// The authorized Submit transaction creates the source and advances the
	// resource permission revision itself. Return its receipt instead of calling
	// that successful write a 403. Subsequent operations need a fresh session.
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"job_id": strconv.FormatInt(queued.JobID, 10),
		"manifest_sha256": queued.ManifestSHA256, "package_sha256": queued.PackageSHA256,
		"item_count": queued.ItemCount, "status": queued.Status, "is_mock": httpapi.IsMockProfile(g.Profile)})
}

type adminConflictError struct{ message string }

func (e *adminConflictError) Error() string { return e.message }

func (g *Handler) adminJobs(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleObserver)
	if !ok {
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT j.id,j.stage,j.status,s.shop_id,j.manifest_sha256,j.dataset_sha256,
		j.fencing_epoch,j.cancel_requested,j.created_at,j.updated_at,count(i.id),
		count(i.id) FILTER (WHERE i.status='pending'),count(i.id) FILTER (WHERE i.status='running'),
		count(i.id) FILTER (WHERE i.status='awaiting_review'),count(i.id) FILTER (WHERE i.status='done'),
		count(i.id) FILTER (WHERE i.status='failed') FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		LEFT JOIN ingest_job_item i ON i.tenant_id=j.tenant_id AND i.job_id=j.id
		WHERE j.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		GROUP BY j.id,s.shop_id ORDER BY j.created_at DESC LIMIT 200`, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion jobs")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, fence, all, pending, running, review, done, failed int64
		var stage, state, shop, manifestHash string
		var datasetHash *string
		var cancelRequested bool
		var created, updated time.Time
		if rows.Scan(&id, &stage, &state, &shop, &manifestHash, &datasetHash, &fence, &cancelRequested,
			&created, &updated, &all, &pending, &running, &review, &done, &failed) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion jobs")
			return
		}
		items = append(items, map[string]any{"job_id": strconv.FormatInt(id, 10), "stage": stage, "status": state,
			"shop_id": shop, "manifest_sha256": manifestHash, "dataset_sha256": datasetHash,
			"fencing_epoch": fence, "cancel_requested": cancelRequested, "item_count": all,
			"pending": pending, "running": running, "awaiting_review": review, "done": done, "failed": failed,
			"created_at": created, "updated_at": updated})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminJobDetail(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleObserver)
	if !ok {
		return
	}
	jobID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, 404, "not_found", "ingestion job not found")
		return
	}
	var jobStatus, jobStage, shopID, manifestHash string
	var datasetHash, errorCode *string
	var fencingEpoch int64
	var cancelRequested bool
	var leaseExpiresAt *time.Time
	err = g.DB.QueryRow(r.Context(), `SELECT j.status,j.stage,s.shop_id,j.manifest_sha256,j.dataset_sha256,
		j.fencing_epoch,j.cancel_requested,j.lease_expires_at,j.error_code FROM ingest_job j
		JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`,
		g.Session.TenantID, jobID, scope.shops).Scan(&jobStatus, &jobStage, &shopID, &manifestHash,
		&datasetHash, &fencingEpoch, &cancelRequested, &leaseExpiresAt, &errorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "ingestion job not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion job")
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT i.id,i.data_id,i.payload_hash,i.status,i.stage,i.retry_count,
		i.error_code,i.cancel_requested,i.fencing_epoch,i.document_version_id::text,v.status,v.review_status,v.source_hash
		FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		JOIN ingest_job_item i ON i.tenant_id=j.tenant_id AND i.job_id=j.id
		LEFT JOIN document_version v ON v.tenant_id=i.tenant_id AND v.id=i.document_version_id
		WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'
		ORDER BY i.id LIMIT 500`, g.Session.TenantID, jobID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion items")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, retries, fence int64
		var dataID, payloadHash, state, stage string
		var errorCode *string
		var cancelled bool
		var versionID, versionState, reviewState, versionHash *string
		if rows.Scan(&id, &dataID, &payloadHash, &state, &stage, &retries, &errorCode, &cancelled,
			&fence, &versionID, &versionState, &reviewState, &versionHash) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion items")
			return
		}
		items = append(items, map[string]any{"item_id": strconv.FormatInt(id, 10), "document_id": dataID,
			"payload_hash": payloadHash, "status": state, "stage": stage, "retry_count": retries,
			"error_code": errorCode, "cancel_requested": cancelled, "fencing_epoch": fence,
			"version_id": versionID, "version_status": versionState, "review_status": reviewState,
			"version_hash": versionHash})
	}
	if err = rows.Err(); err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion items")
		return
	}
	if len(items) == 0 {
		var exists bool
		if err = g.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
			WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest')`,
			g.Session.TenantID, jobID, scope.shops).Scan(&exists); err != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not verify ingestion job")
			return
		}
		if !exists {
			httpapi.WriteError(w, 404, "not_found", "ingestion job not found")
			return
		}
	}
	httpapi.WriteJSON(w, 200, map[string]any{"job_id": strconv.FormatInt(jobID, 10), "status": jobStatus,
		"stage": jobStage, "shop_id": shopID, "manifest_sha256": manifestHash, "dataset_sha256": datasetHash,
		"fencing_epoch": fencingEpoch, "cancel_requested": cancelRequested, "lease_expires_at": leaseExpiresAt,
		"error_code": errorCode, "items": items})
}

func (g *Handler) adminReviews(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleReviewer)
	if !ok {
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT v.id,d.logical_key,d.title,s.shop_id,v.source_hash,v.status,v.Disclosure_class,
		v.external_allowed,v.review_status,v.reviewed_hash,v.fencing_epoch,v.chunk_count,v.pipeline_version,v.quality_json,v.created_at,
		j.id,j.stage FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		LEFT JOIN ingest_job_item i ON i.tenant_id=v.tenant_id AND i.document_version_id=v.id
		LEFT JOIN ingest_job j ON j.tenant_id=i.tenant_id AND j.id=i.job_id
		WHERE v.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		ORDER BY v.created_at DESC LIMIT 300`, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load review queue")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var jobID *int64
		var key, title, shop, hash, state, disclosure, reviewState, pipeline, stage string
		var external bool
		var reviewedHash *string
		var fence int64
		var chunks *int32
		var created time.Time
		var quality []byte
		if rows.Scan(&id, &key, &title, &shop, &hash, &state, &disclosure, &external, &reviewState, &reviewedHash, &fence, &chunks, &pipeline, &quality, &created, &jobID, &stage) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load review queue")
			return
		}
		items = append(items, map[string]any{"version_id": strconv.FormatInt(id, 10), "document_id": key, "title": title, "shop_id": shop,
			"source_hash": hash, "status": state, "disclosure_class": disclosure, "external_allowed": external, "review_status": reviewState,
			"reviewed_hash": reviewedHash, "fencing_epoch": fence, "chunk_count": chunks, "pipeline_version": pipeline,
			"quality": json.RawMessage(quality), "created_at": created,
			"job_id": jobID, "stage": stage})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminReviewDetail(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleReviewer)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, 404, "not_found", "version not found")
		return
	}
	var data map[string]any
	err = g.DB.QueryRow(r.Context(), `SELECT jsonb_build_object('version_id',v.id::text,'document_id',d.logical_key,'title',d.title,
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
		JOIN source_file sf ON sf.tenant_id=v.tenant_id AND sf.source_manifest_id=v.source_manifest_id AND sf.document_id=d.logical_key
		WHERE v.tenant_id=$1 AND v.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`,
		g.Session.TenantID, id, scope.shops).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "version not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "review material is unavailable")
		return
	}
	content, err := projectReviewContent(data["content"])
	if err != nil {
		httpapi.WriteError(w, 503, "review_content_unavailable", "review material is unavailable")
		return
	}
	data["content"] = content
	if err = g.Authority.Refresh(r.Context()); err != nil {
		httpapi.WriteError(w, auth.HTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return
	}
	httpapi.WriteJSON(w, 200, data)
}

func (g *Handler) adminReviewVersion(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleReviewer)
	if !ok {
		return
	}
	var in struct {
		Confirm          bool   `json:"confirm"`
		Decision         string `json:"decision"`
		Disclosure       string `json:"disclosure_class"`
		External         bool   `json:"external_allowed"`
		ExpectedHash     string `json:"expected_hash"`
		ExpectedFence    int64  `json:"expected_fencing_epoch"`
		ExpectedRevision string `json:"permission_revision"`
		Reason           string `json:"reason"`
	}
	if httpapi.DecodeJSON(r, &in) != nil || !in.Confirm || !contains([]string{"approved", "rejected"}, in.Decision) || !contains([]string{"external_allowed", "internal_only", "unclassified"}, in.Disclosure) || len(strings.TrimSpace(in.Reason)) < 10 || len(in.Reason) > 2000 || in.ExpectedFence < 0 {
		httpapi.WriteError(w, 400, "invalid_review", "review decision, classification and reason are required")
		return
	}
	if in.External != (in.Disclosure == "external_allowed") || (in.Decision == "approved" && in.Disclosure == "unclassified") {
		httpapi.WriteError(w, 400, "invalid_disclosure", "review classification is inconsistent")
		return
	}
	if in.ExpectedRevision != g.Authority.CurrentRevision() {
		httpapi.WriteError(w, 409, "confirmation_stale", "permission revision changed")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpapi.WriteError(w, 404, "not_found", "version not found")
		return
	}
	shop, _, hash, _, _, _, err := g.lockVersion(r.Context(), id, scope.shops)
	if err != nil {
		g.writeVersionError(w, err)
		return
	}
	if hash != in.ExpectedHash {
		httpapi.WriteError(w, 409, "version_conflict", "version hash changed")
		return
	}
	g.dispatchCommand(w, r, shop, &ragv1.ExecuteCommandRequest{Action: ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_REVIEW, ResourceId: strconv.FormatInt(id, 10), ExpectedHash: in.ExpectedHash, ExpectedFencingEpoch: in.ExpectedFence, Reason: strings.TrimSpace(in.Reason), ReviewDecision: in.Decision, DisclosureClass: in.Disclosure, ExternalAllowed: in.External})
}

func (g *Handler) adminPublishVersion(w http.ResponseWriter, r *http.Request) {
	g.adminVersionCommand(w, r, ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_PUBLISH)
}
func (g *Handler) adminRollbackVersion(w http.ResponseWriter, r *http.Request) {
	g.adminVersionCommand(w, r, ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_ROLLBACK)
}
func (g *Handler) adminRevokeVersion(w http.ResponseWriter, r *http.Request) {
	g.adminVersionCommand(w, r, ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_REVOKE)
}

func (g *Handler) adminVersions(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleObserver)
	if !ok {
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT v.id,d.logical_key,d.title,s.shop_id,v.status,v.review_status,v.source_hash,v.reviewed_hash,
		v.Disclosure_class,v.external_allowed,v.fencing_epoch,v.chunk_count,v.created_at,v.activated_at
		FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		ORDER BY v.created_at DESC LIMIT 500`, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load version history")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var key, title, shop, state, review, hash, disclosure string
		var reviewed *string
		var external bool
		var fence int64
		var chunks *int32
		var created time.Time
		var active *time.Time
		if rows.Scan(&id, &key, &title, &shop, &state, &review, &hash, &reviewed, &disclosure, &external, &fence, &chunks, &created, &active) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load version history")
			return
		}
		items = append(items, map[string]any{"version_id": strconv.FormatInt(id, 10), "document_id": key, "title": title, "shop_id": shop, "status": state, "review_status": review, "source_hash": hash, "reviewed_hash": reviewed, "disclosure_class": disclosure, "external_allowed": external, "fencing_epoch": fence, "chunk_count": chunks, "created_at": created, "activated_at": active})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) lockVersion(ctx context.Context, id int64, shops []string) (shop, key, hash string, fence int64, state, review string, err error) {
	err = g.DB.QueryRow(ctx, `SELECT s.shop_id,d.logical_key,v.source_hash,v.fencing_epoch,v.status,v.review_status FROM document_version v
		JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND v.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`, g.Session.TenantID, id, shops).Scan(&shop, &key, &hash, &fence, &state, &review)
	if errors.Is(err, pgx.ErrNoRows) {
		err = pgx.ErrNoRows
	}
	return
}

func (g *Handler) confirmVersion(w http.ResponseWriter, r *http.Request, in adminConfirmation, shop, key, hash string, fence int64) bool {
	if in.TenantID != g.Session.Tenant || in.ShopID != shop || in.DocumentID != key || in.VersionHash != hash || in.PermissionRevision != g.Authority.CurrentRevision() || in.ExpectedFencingEpoch != fence {
		httpapi.WriteError(w, 409, "confirmation_stale", "confirmation does not match the current authorized resource")
		return false
	}
	if err := g.Authority.Refresh(r.Context()); err != nil {
		httpapi.WriteError(w, auth.HTTPStatus(err), auth.FailureCode(err), "authorization failed")
		return false
	}
	return true
}

func (g *Handler) adminGrantInTx(ctx context.Context, tx pgx.Tx, role, shop string) bool {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT ecr_check_admin_grant($1,$2,$3,$4)`,
		g.Session.TenantID, g.Session.UserID, role, shop).Scan(&allowed)
	return err == nil && allowed
}

func (g *Handler) writeVersionError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "version not found")
	} else {
		httpapi.WriteError(w, 503, "database_unavailable", "version status is unavailable")
	}
}

func auditAdmin(ctx context.Context, tx pgx.Tx, tenant, user int64, action, kind, resource, version, hash string, fence int64, reason, result string, metadata map[string]any) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	var versionValue, hashValue, reasonValue any
	if version != "" {
		versionValue = version
	}
	if hash != "" {
		hashValue = hash
	}
	if reason != "" {
		reasonValue = reason
	}
	var fenceValue any
	if fence > 0 {
		fenceValue = fence
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,resource_version,content_hash,fencing_epoch,reason,result,metadata_json)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb)`, tenant, user, action, kind, resource, versionValue, hashValue, fenceValue, reasonValue, result, encoded)
	return err
}

func validAdminRequest(role, scope string, shops []string, reason string, allowed []string) bool {
	if !contains([]string{"platform_observer", "merchant_admin", "knowledge_reviewer"}, role) || len(strings.TrimSpace(reason)) < 10 || len(reason) > 2000 {
		return false
	}
	if scope == "tenant" {
		return len(shops) == 0
	}
	if scope != "shops" || len(shops) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, shop := range shops {
		if !contains(allowed, shop) || seen[shop] {
			return false
		}
		seen[shop] = true
	}
	return true
}

func databaseDecisionStatus(err error) int {
	if errors.Is(err, pgx.ErrNoRows) {
		return 409
	}
	if status.Code(err) == codes.PermissionDenied {
		return 403
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "42501":
			return 403
		case "40001", "23505", "23503", "23514", "55000":
			return 409
		}
	}
	return 503
}
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func allContained(allowed, requested []string) bool {
	for _, value := range requested {
		if !contains(allowed, value) {
			return false
		}
	}
	return true
}
func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
