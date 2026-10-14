package auth

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const customerDocumentsSQL = `SELECT d.logical_key FROM document d
 JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
 JOIN document_version v ON v.tenant_id=d.tenant_id AND v.id=d.active_version_id
 WHERE d.tenant_id=$1 AND s.shop_id=$2 AND d.status='active' AND v.status='active'
 AND v.review_status='approved' AND v.external_allowed AND v.disclosure_class='external_allowed'
 AND NOT EXISTS (SELECT 1 FROM (VALUES ('tenant',$1),('source',s.id),('document',d.id)) gate(kind,id)
   WHERE EXISTS(SELECT 1 FROM acl a WHERE a.tenant_id=$1 AND a.resource_type=gate.kind AND a.resource_id=gate.id)
   AND NOT EXISTS(SELECT 1 FROM acl a WHERE a.tenant_id=$1 AND a.resource_type=gate.kind AND a.resource_id=gate.id
      AND a.subject_type='role' AND a.subject_id='customer' AND a.permission='read')) ORDER BY d.logical_key`

func CustomerDocuments(ctx context.Context, db *pgxpool.Pool, tenant int64, shop string) ([]string, error) {
	rows, err := db.Query(ctx, customerDocumentsSQL, tenant, shop)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func IssueCustomerScope(ctx context.Context, db *pgxpool.Pool, key []byte, sessionID, conversationID, executionID string) (*ragv1.RequestContext, error) {
	scope := &ragv1.RequestContext{RequestId: executionID, ConversationId: conversationID, TurnId: executionID, ExecutionId: executionID,
		SubjectType: "customer", CustomerSessionId: sessionID, Role: "customer", ScopeAudience: "ai", EnforceDocumentScope: true}
	err := db.QueryRow(ctx, `SELECT c.tenant_id,t.external_id,c.shop_id,c.customer_id,c.epoch,sh.public_permission_revision
 FROM customer_conversation c JOIN customer_session cs ON cs.customer_id=c.customer_id AND cs.tenant_id=c.tenant_id AND cs.shop_id=c.shop_id
 JOIN tenant t ON t.id=c.tenant_id JOIN shop sh ON sh.tenant_id=c.tenant_id AND sh.id=c.shop_id
 WHERE c.id=$1 AND cs.id=$2 AND c.state='AI' AND c.active_execution=$3 AND cs.revoked_at IS NULL
 AND cs.expires_at>now() AND cs.idle_until>now() AND t.status='active' AND sh.status='active' AND sh.customer_chat_enabled`, conversationID, sessionID, executionID).
		Scan(&scope.TenantDbId, &scope.TenantId, &scope.ShopId, &scope.CustomerId, &scope.ConversationEpoch, &scope.PermissionRevision)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, "customer_scope_invalid")
	}
	scope.UserId = "customer:" + scope.CustomerId
	scope.AllowedShopIds = []string{scope.ShopId}
	scope.AllowedDocumentIds, err = CustomerDocuments(ctx, db, scope.TenantDbId, scope.ShopId)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "authorization_unavailable")
	}
	if len(scope.AllowedDocumentIds) == 0 {
		return nil, errors.New("knowledge_missing")
	}
	scope.ScopeExpiresAt = time.Now().Add(ScopeTTL).Unix()
	scope.ScopeSignature = hex.EncodeToString(MAC(key, scope))
	return scope, nil
}

func ValidateCustomerScope(ctx context.Context, db *pgxpool.Pool, scope *ragv1.RequestContext) error {
	if scope.ScopeAudience != "ai" || scope.Role != "customer" || scope.UserDbId != 0 || scope.AllShops || len(scope.AllowedShopIds) != 1 || scope.AllowedShopIds[0] != scope.ShopId || scope.UserId != "customer:"+scope.CustomerId || scope.CustomerId == "" || scope.CustomerSessionId == "" {
		return status.Error(codes.PermissionDenied, "invalid_customer_scope")
	}
	current, err := IssueCustomerScope(ctx, db, make([]byte, 32), scope.CustomerSessionId, scope.ConversationId, scope.ExecutionId)
	if err != nil {
		return status.Error(codes.PermissionDenied, "customer_scope_stale")
	}
	if current.CustomerId != scope.CustomerId || current.TenantDbId != scope.TenantDbId || current.TenantId != scope.TenantId || current.ShopId != scope.ShopId || current.PermissionRevision != scope.PermissionRevision || current.ConversationEpoch != scope.ConversationEpoch {
		return status.Error(codes.PermissionDenied, "customer_scope_stale")
	}
	for _, id := range scope.AllowedDocumentIds {
		if !slices.Contains(current.AllowedDocumentIds, id) {
			return status.Error(codes.PermissionDenied, "customer_document_forbidden")
		}
	}
	return nil
}

func CustomerEvidenceAllowed(ctx context.Context, db *pgxpool.Pool, tenant int64, shop string, item *ragv1.Evidence) (bool, error) {
	if item == nil || item.DocumentId == "" || item.VersionId == "" || item.ShopId != shop || item.DisclosureClass != "external_allowed" || !item.CustomerEligible {
		return false, nil
	}
	ids, err := CustomerDocuments(ctx, db, tenant, shop)
	if err != nil || !slices.Contains(ids, item.DocumentId) {
		return false, err
	}
	var canonical bool
	err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chunk c
 JOIN document d ON d.id=c.doc_id AND d.tenant_id=c.tenant_id
 JOIN tenant t ON t.id=c.tenant_id
 WHERE c.tenant_id=$1 AND c.shop_id=$2 AND d.logical_key=$3 AND d.active_version_id=c.version_id
 AND c.version_id::text=$4 AND 'db-chunk-'||c.id::text=$5 AND c.content=$6 AND t.external_id=$7
 AND c.content_type<>'heading' AND length(btrim(c.content))>0
 AND (c.effective_from IS NULL OR c.effective_from<=CURRENT_DATE)
 AND (c.effective_to IS NULL OR c.effective_to>CURRENT_DATE))`, tenant, shop, item.DocumentId, item.VersionId, item.Id, item.Content, item.TenantId).Scan(&canonical)
	return err == nil && canonical, err
}
