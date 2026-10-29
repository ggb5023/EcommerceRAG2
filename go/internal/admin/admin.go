package admin

import (
	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"

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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type Handler struct {
	DB           *pgxpool.Pool
	Session      *auth.Session
	Authority    *auth.Authority
	Ingest       ragv1.IngestServiceClient
	AIConnection *grpc.ClientConn
	Profile      string
	SigningKey   []byte
	// Set only from the initialized customer service's validated configuration.
	CustomerOrigin string
}

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
		scopeKind, shops, err := adminStore.RoleGrantScope(r.Context(), g.DB, g.Session.TenantID, g.Session.UserID, role)
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
	scopeKind, shops, err := adminStore.RequestDecisionScope(r.Context(), g.DB, g.Session.TenantID,
		g.Session.UserID, shopID, []string{adminRoleAccess, adminRoleMerchant})
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
	roles, err := adminStore.ActiveRoleGrants(r.Context(), g.DB, g.Session.TenantID, g.Session.UserID)
	if err != nil {
		httpapi.WriteError(w, 503, "authorization_unavailable", "could not load administrative session")
		return
	}
	entries, err := adminStore.RoleCatalog(r.Context(), g.DB)
	if err != nil {
		httpapi.WriteError(w, 503, "authorization_unavailable", "could not load administrative role catalog")
		return
	}
	catalog := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		catalog = append(catalog, map[string]any{
			"role_key": entry.RoleKey, "storage_role": entry.StorageRole, "display_name": entry.DisplayName,
			"catalog_version": entry.CatalogVersion, "enabled": entry.Enabled, "requestable": entry.Requestable,
		})
	}
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
	worker, err := adminStore.IngestWorkerStatus(ctx, g.DB)
	if err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load ingestion worker status")
		return
	}
	workerRows, activeWorkers := worker.Rows, worker.ActiveWorkers
	workerHeartbeat, degradedWorkers, workerErrorCode := worker.HeartbeatAt, worker.DegradedWorkers, worker.LastErrorCode
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
	migrationVersion, legacyMigrations, migrationErr := adminStore.MigrationState(ctx, g.DB)
	if migrationErr != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load migration status")
		return
	}
	jobs, pending, failed, err := adminStore.IngestJobCounts(ctx, g.DB, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load ingestion status")
		return
	}
	executionsLast, errorsLast, err := adminStore.RecentExecutionErrors(ctx, g.DB, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load error rate")
		return
	}
	versionCounts, err := adminStore.VersionStatusCounts(ctx, g.DB, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "status_unavailable", "could not load version status")
		return
	}
	var versionTotal, activeVersions int64
	for state, count := range versionCounts {
		versionTotal += count
		if state == "active" {
			activeVersions = count
		}
	}
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
	accounts, err := adminStore.Accounts(r.Context(), g.DB, g.Session.TenantID, scope.shops, scope.tenantWide)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load account summaries")
		return
	}
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, map[string]any{"user_id": strconv.FormatInt(account.ID, 10), "external_id": account.ExternalID,
			"display_name": account.DisplayName, "runtime_role": account.Role, "shop_ids": account.ShopIDs,
			"permission_revision": account.PermissionRevision, "revoked": account.RevokedAt != nil,
			"grants": json.RawMessage(account.Grants)})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminListRequests(w http.ResponseWriter, r *http.Request) {
	scope, canManageRequests, ok := g.permissionRequestScope(w, r)
	if !ok {
		return
	}
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	requests, err := adminStore.PermissionRequests(r.Context(), g.DB, g.Session.TenantID, statusFilter,
		scope.shops, scope.tenantWide, canManageRequests, g.Session.UserID)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load permission requests")
		return
	}
	items := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		items = append(items, map[string]any{"id": strconv.FormatInt(request.ID, 10),
			"requested_by": strconv.FormatInt(request.RequestedBy, 10), "requester_name": request.RequesterName,
			"role": request.Role, "scope_kind": request.ScopeKind, "shop_ids": request.ShopIDs,
			"reason": request.Reason, "status": request.Status, "reviewed_by": request.ReviewedBy,
			"reviewed_at": request.ReviewedAt, "decision_reason": request.DecisionReason,
			"created_at": request.CreatedAt, "payload_hash": request.PayloadHash})
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
	requestable, catalogErr := adminStore.RoleCatalogRequestable(r.Context(), g.DB, in.Role)
	if err := catalogErr; errors.Is(err, pgx.ErrNoRows) {
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
	id, err := adminStore.CreatePermissionRequest(r.Context(), tx, PermissionRequest{
		TenantID: g.Session.TenantID, RequestedBy: g.Session.UserID, Role: in.Role, ScopeKind: in.Scope,
		ShopIDs: in.Shops, Reason: strings.TrimSpace(in.Reason), PayloadHash: hex.EncodeToString(sum[:]),
	})
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
	target, err := adminStore.PendingPermissionRequest(r.Context(), tx, g.Session.TenantID, requestID, g.Session.UserID)
	targetUser, role, scopeKind, shops := target.RequestedBy, target.Role, target.ScopeKind, target.ShopIDs
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
	if err = adminStore.DecidePermissionRequest(r.Context(), tx, PermissionDecision{
		TenantID: g.Session.TenantID, RequestID: requestID, ActorID: g.Session.UserID,
		Decision: in.Decision, Reason: strings.TrimSpace(in.Reason),
		TargetUser: targetUser, Role: role, ScopeKind: scopeKind, ShopIDs: shops,
	}); err != nil {
		if in.Decision == "approved" {
			httpapi.WriteError(w, databaseDecisionStatus(err), "request_conflict", "permission grant could not be issued")
			return
		}
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
	records, err := adminStore.AuditRecords(r.Context(), g.DB, g.Session.TenantID, scope.shops, scope.tenantWide)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load audit records")
		return
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, map[string]any{"id": strconv.FormatInt(record.ID, 10),
			"actor_user_id": strconv.FormatInt(record.ActorUserID, 10), "action": record.Action,
			"resource_type": record.ResourceType, "resource_id": record.ResourceID,
			"resource_version": record.ResourceVersion, "content_hash": record.ContentHash,
			"fencing_epoch": record.FencingEpoch, "reason": record.Reason, "result": record.Result,
			"metadata": json.RawMessage(record.Metadata), "created_at": record.CreatedAt})
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
	err = adminStore.RevokeGrant(r.Context(), g.DB, g.Session.TenantID, id, g.Session.UserID, strings.TrimSpace(in.Reason))
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
	shops, err := adminStore.Shops(r.Context(), g.DB, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load shops")
		return
	}
	items := make([]map[string]any, 0, len(shops))
	for _, shop := range shops {
		items = append(items, map[string]any{"shop_id": shop.ID, "name": shop.Name, "status": shop.Status,
			"public_slug": shop.PublicSlug, "welcome_message": shop.WelcomeMessage,
			"customer_chat_enabled": shop.CustomerChatEnabled,
			"customer_chat_url":     customerChatURL(g.CustomerOrigin, shop.PublicSlug, shop.CustomerChatEnabled && shop.Status == "active")})
	}
	// Reuse customer authorization; a published internal or ACL-blocked document
	// must never be advertised as available for a customer trial.
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

func (g *Handler) adminGrantInTx(ctx context.Context, tx pgx.Tx, role, shop string) bool {
	allowed, err := adminStore.IssueAdminGrant(ctx, tx, GrantRequest{
		TenantID: g.Session.TenantID, UserID: g.Session.UserID, Role: role, Shop: shop,
	})
	return err == nil && allowed
}

func auditAdmin(ctx context.Context, tx pgx.Tx, tenant, user int64, action, kind, resource, version, hash string, fence int64, reason, result string, metadata map[string]any) error {
	return adminStore.AppendAudit(ctx, tx, AuditEntry{
		TenantID: tenant, UserID: user, Action: action, ResourceType: kind, ResourceID: resource,
		ResourceVersion: version, ContentHash: hash, FencingEpoch: fence, Reason: reason,
		Result: result, Metadata: metadata,
	})
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
