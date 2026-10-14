package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const ScopeTTL = 65 * time.Second

type Session struct {
	TenantID           int64
	UserID             int64
	Tenant             string
	User               string
	Shop               string
	AllowedShops       []string
	AllShops           bool
	Role               string
	PermissionRevision string
	RevokedAt          *time.Time
	RevisionPinned     bool
}
type Resource struct{ Shop, Disclosure string }
type Authority struct {
	DB                    *pgxpool.Pool
	Session               *Session
	SigningKey            []byte
	RevisionMu            *sync.RWMutex
	IdentityCheck         func(context.Context) error
	RegisterScopeIdentity func(*ragv1.RequestContext) error
	ValidateScopeIdentity func(context.Context, *ragv1.RequestContext) error
}

func LoadSigningKey() ([]byte, error) {
	path := os.Getenv("SCOPE_SIGNING_KEY_FILE")
	if path == "" {
		path = "/etc/ecommerce-rag/synthetic-auth.key"
	}
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	owner, ok := stat.Sys().(*syscall.Stat_t)
	if !ok || !stat.Mode().IsRegular() || stat.Mode().Perm() != 0600 || owner.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("scope key must be an owned regular 0600 file")
	}
	key, err := os.ReadFile(path)
	if err != nil || len(key) < 32 {
		return nil, errors.New("scope key is missing or too short")
	}
	return key, nil
}

// Identity fields are immutable; development policy revisions may refresh.
// OIDC requests pin the revision from the verified token and fail on changes.
func (g *Authority) Refresh(ctx context.Context) error {
	if g.IdentityCheck != nil {
		if err := g.IdentityCheck(ctx); err != nil {
			return err
		}
	}
	if g.DB == nil {
		return nil
	}
	var role, revision, externalID, tenantStatus string
	var revoked *time.Time
	var shops []string
	err := g.DB.QueryRow(ctx, `SELECT u.Role,COALESCE(u.shop_ids,ARRAY[]::text[]),u.permission_revision,u.revoked_at,u.external_id,t.status
      FROM app_user u JOIN tenant t ON t.id=u.tenant_id WHERE u.tenant_id=$1 AND u.id=$2`, g.Session.TenantID, g.Session.UserID).
		Scan(&role, &shops, &revision, &revoked, &externalID, &tenantStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return status.Error(codes.PermissionDenied, "identity_not_found")
	}
	if err != nil {
		return status.Error(codes.Unavailable, "authorization_unavailable")
	}
	if revoked != nil || tenantStatus != "active" {
		return status.Error(codes.PermissionDenied, "identity_revoked")
	}
	if role != g.Session.Role || externalID != g.Session.User || !slices.Equal(shops, g.Session.AllowedShops) {
		return status.Error(codes.PermissionDenied, "permission_changed")
	}
	if !slices.Contains([]string{"owner", "admin", "operator", "viewer"}, role) {
		return status.Error(codes.PermissionDenied, "role_forbidden")
	}
	if len(shops) == 0 {
		return status.Error(codes.PermissionDenied, "no_shop_authorized")
	}
	if !slices.Contains(shops, g.Session.Shop) || (g.Session.AllShops && role != "owner" && role != "admin") {
		return status.Error(codes.PermissionDenied, "shop_not_authorized")
	}
	var active bool
	if err = g.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shop WHERE tenant_id=$1 AND id=$2 AND status='active')`, g.Session.TenantID, g.Session.Shop).Scan(&active); err != nil {
		return status.Error(codes.Unavailable, "authorization_unavailable")
	}
	if !active {
		return status.Error(codes.PermissionDenied, "shop_not_authorized")
	}
	// The mock gateway resolves identity from a fixed development adapter, not
	// from a browser token. Refresh policy revision after revalidating the full
	// identity tuple so source/document ACL changes do not strand the console.
	g.RevisionMu.Lock()
	if g.Session.RevisionPinned && g.Session.PermissionRevision != revision {
		g.RevisionMu.Unlock()
		return status.Error(codes.PermissionDenied, "permission_changed")
	}
	g.Session.PermissionRevision = revision
	g.RevisionMu.Unlock()
	return nil
}

func (g *Authority) CurrentRevision() string {
	g.RevisionMu.RLock()
	defer g.RevisionMu.RUnlock()
	return g.Session.PermissionRevision
}

func (g *Authority) Documents(ctx context.Context) (map[string]Resource, error) {
	return g.DocumentsForShop(ctx, g.Session.Shop)
}

func (g *Authority) DocumentsForShop(ctx context.Context, shop string) (map[string]Resource, error) {
	rows, err := g.DB.Query(ctx, `SELECT d.logical_key,s.shop_id,COALESCE(d.meta_json->>'disclosure_class','unclassified')
      FROM document d JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
      JOIN tenant t ON t.id=d.tenant_id
      JOIN shop sh ON sh.tenant_id=d.tenant_id AND sh.id=s.shop_id
      WHERE d.tenant_id=$1 AND d.status='active' AND t.status='active' AND sh.status='active'
        AND s.shop_id=$2 AND s.shop_id=ANY($3::text[])
        AND NOT EXISTS (
          SELECT 1 FROM (VALUES ('tenant',t.id),('source',s.id),('document',d.id)) gate(kind,id)
          WHERE EXISTS (SELECT 1 FROM acl a WHERE a.tenant_id=d.tenant_id AND a.resource_type=gate.kind AND a.resource_id=gate.id)
            AND NOT EXISTS (SELECT 1 FROM acl a WHERE a.tenant_id=d.tenant_id AND a.resource_type=gate.kind AND a.resource_id=gate.id
              AND a.permission IN ('read','write','admin') AND
              ((a.subject_type='user' AND a.subject_id IN ($4,$5)) OR (a.subject_type='role' AND a.subject_id=$6))))`,
		g.Session.TenantID, shop, g.Session.AllowedShops, g.Session.User, strconv.FormatInt(g.Session.UserID, 10), g.Session.Role)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "authorization_unavailable")
	}
	defer rows.Close()
	result := map[string]Resource{}
	for rows.Next() {
		var id string
		var resource Resource
		if rows.Scan(&id, &resource.Shop, &resource.Disclosure) != nil {
			return nil, status.Error(codes.Unavailable, "authorization_unavailable")
		}
		result[id] = resource
	}
	if rows.Err() != nil {
		return nil, status.Error(codes.Unavailable, "authorization_unavailable")
	}
	return result, nil
}

func (g *Authority) EvidenceAllowed(ctx context.Context, item *ragv1.Evidence) (bool, error) {
	if item == nil || item.DocumentId == "" || (item.TenantId != "" && item.TenantId != g.Session.Tenant) {
		return false, nil
	}
	if g.DB == nil {
		return slices.Contains(g.Session.AllowedShops, item.ShopId), nil
	}
	resources, err := g.Documents(ctx)
	if err != nil {
		return false, err
	}
	resource, found := resources[item.DocumentId]
	if !found || resource.Shop != item.ShopId || resource.Disclosure != "external_allowed" ||
		(item.DisclosureClass != "" && item.DisclosureClass != resource.Disclosure) {
		return false, nil
	}
	if g.DB != nil {
		var managed bool
		var activeVersion *string
		err = g.DB.QueryRow(ctx, `SELECT s.type='admin_manifest',active.id::text FROM document d
			JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		LEFT JOIN document_version active ON active.tenant_id=d.tenant_id AND active.id=d.active_version_id
		WHERE d.tenant_id=$1 AND d.logical_key=$2 AND s.shop_id=$3`,
			g.Session.TenantID, item.DocumentId, item.ShopId).Scan(&managed, &activeVersion)
		if err != nil {
			return false, status.Error(codes.Unavailable, "authorization_unavailable")
		}
		if !ActiveEvidenceVersion(managed, activeVersion, item.VersionId) {
			return false, nil
		}
	}
	return true, nil
}

func ActiveEvidenceVersion(managed bool, activeVersion *string, evidenceVersion string) bool {
	if !managed {
		return true
	}
	return activeVersion != nil && evidenceVersion != "" && evidenceVersion == *activeVersion
}

func MAC(key []byte, scope *ragv1.RequestContext) []byte {
	unsigned := proto.Clone(scope).(*ragv1.RequestContext)
	unsigned.ScopeSignature = ""
	data, _ := proto.MarshalOptions{Deterministic: true}.Marshal(unsigned)
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func (g *Authority) IssueScope(ctx context.Context, requestID string) (*ragv1.RequestContext, error) {
	return g.IssueScopeForShopWithTurn(ctx, requestID, g.Session.Shop, "", "", "")
}

func (g *Authority) IssueScopeForShop(ctx context.Context, requestID, shop string) (*ragv1.RequestContext, error) {
	return g.IssueScopeForShopWithTurn(ctx, requestID, shop, "", "", "")
}

func (g *Authority) IssueScopeForTurn(ctx context.Context, requestID string, conversationID, turnID, executionID int64) (*ragv1.RequestContext, error) {
	return g.IssueScopeForShopWithTurn(ctx, requestID, g.Session.Shop,
		strconv.FormatInt(conversationID, 10), strconv.FormatInt(turnID, 10), strconv.FormatInt(executionID, 10))
}

func (g *Authority) IssueScopeForShopWithTurn(ctx context.Context, requestID, shop, conversationID, turnID, executionID string) (*ragv1.RequestContext, error) {
	if !slices.Contains(g.Session.AllowedShops, shop) {
		return nil, status.Error(codes.PermissionDenied, "shop_not_authorized")
	}
	if err := g.Refresh(ctx); err != nil {
		return nil, err
	}
	resources, err := g.DocumentsForShop(ctx, shop)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for id, resource := range resources {
		if resource.Disclosure == "external_allowed" {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	scope := &ragv1.RequestContext{RequestId: requestID, TenantId: g.Session.Tenant, UserId: g.Session.User,
		ShopId: shop, Role: g.Session.Role, AllowedShopIds: append([]string(nil), g.Session.AllowedShops...),
		AllShops: g.Session.AllShops, PermissionRevision: g.CurrentRevision(),
		AllowedDocumentIds: ids, EnforceDocumentScope: true, ScopeExpiresAt: time.Now().Add(ScopeTTL).Unix(),
		TenantDbId: g.Session.TenantID, UserDbId: g.Session.UserID,
		ConversationId: conversationID, TurnId: turnID, ExecutionId: executionID, ScopeAudience: "ai"}
	scope.ScopeSignature = hex.EncodeToString(MAC(g.SigningKey, scope))
	if g.RegisterScopeIdentity != nil {
		if err = g.RegisterScopeIdentity(scope); err != nil {
			return nil, err
		}
	}
	if err = g.Refresh(ctx); err != nil {
		return nil, err
	}
	return scope, nil
}

func (g *Authority) ValidateScope(ctx context.Context, scope *ragv1.RequestContext) error {
	signature, err := hex.DecodeString(scope.ScopeSignature)
	now := time.Now().Unix()
	if err != nil || len(g.SigningKey) < 32 || !hmac.Equal(signature, MAC(g.SigningKey, scope)) ||
		scope.ScopeExpiresAt <= now || scope.ScopeExpiresAt > now+70 || !scope.EnforceDocumentScope || scope.RequestId == "" {
		return status.Error(codes.PermissionDenied, "invalid_scope")
	}
	if scope.SubjectType == "customer" {
		return ValidateCustomerScope(ctx, g.DB, scope)
	}
	if scope.SubjectType != "" && scope.SubjectType != "staff" {
		return status.Error(codes.PermissionDenied, "invalid_subject_type")
	}
	if g.ValidateScopeIdentity != nil {
		if err = g.ValidateScopeIdentity(ctx, scope); err != nil {
			return err
		}
	}
	var tenant string
	err = g.DB.QueryRow(ctx, `SELECT external_id FROM tenant WHERE id=$1`, scope.TenantDbId).Scan(&tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return status.Error(codes.PermissionDenied, "invalid_scope")
	}
	if err != nil {
		return status.Error(codes.Unavailable, "authorization_unavailable")
	}
	if tenant == "" || tenant != scope.TenantId {
		return status.Error(codes.PermissionDenied, "invalid_scope")
	}
	verifier := &Authority{DB: g.DB, RevisionMu: &sync.RWMutex{}, Session: &Session{TenantID: scope.TenantDbId, UserID: scope.UserDbId,
		Tenant: scope.TenantId, User: scope.UserId, Shop: scope.ShopId, Role: scope.Role,
		AllowedShops: scope.AllowedShopIds, AllShops: scope.AllShops, PermissionRevision: scope.PermissionRevision}}
	if err = verifier.Refresh(ctx); err != nil {
		return err
	}
	if verifier.CurrentRevision() != scope.PermissionRevision {
		return status.Error(codes.PermissionDenied, "permission_changed")
	}
	if scope.ScopeAudience == "ingest" {
		return nil
	}
	if scope.ScopeAudience != "ai" {
		return status.Error(codes.PermissionDenied, "invalid_scope_audience")
	}
	resources, err := verifier.Documents(ctx)
	if err != nil {
		return err
	}
	for _, id := range scope.AllowedDocumentIds {
		resource, found := resources[id]
		if !found || resource.Disclosure != "external_allowed" {
			return status.Error(codes.PermissionDenied, "invalid_scope")
		}
	}
	if err := verifier.Refresh(ctx); err != nil {
		return err
	}
	if verifier.CurrentRevision() != scope.PermissionRevision {
		return status.Error(codes.PermissionDenied, "permission_changed")
	}
	return nil
}

func FailureCode(err error) string {
	switch status.Code(err) {
	case codes.PermissionDenied:
		if strings.Contains(err.Error(), "identity_revoked") {
			return "identity_revoked"
		}
		if strings.Contains(err.Error(), "permission_changed") {
			return "permission_changed"
		}
		if strings.Contains(err.Error(), "shop_not_authorized") {
			return "shop_not_authorized"
		}
		if strings.Contains(err.Error(), "no_shop_authorized") {
			return "no_shop_authorized"
		}
		return "permission_denied"
	case codes.Unavailable:
		return "authorization_unavailable"
	default:
		return "authorization_failed"
	}
}

func HTTPStatus(err error) int {
	if status.Code(err) == codes.PermissionDenied {
		return 403
	}
	return 503
}
