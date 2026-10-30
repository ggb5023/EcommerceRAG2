package gateway

import (
	"context"
	"slices"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"

	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
)

// loadHistorySummary reads the bounded history of earlier turns that share the
// current permission revision. A failed read degrades to an empty history, which
// is what the inline version did.
func loadHistorySummary(ctx context.Context, db *pgxpool.Pool, tenantID int64, permissionRevision string,
	conversationID, currentTurnID int64) string {
	rows, err := db.Query(ctx, `SELECT m.Role, COALESCE(m.content,'')
		FROM message m JOIN turn t ON t.tenant_id=m.tenant_id AND t.id=m.turn_id
		WHERE t.tenant_id=$1 AND t.conversation_id=$2 AND t.id<>$3 AND t.status IN ('DONE','ASKING') AND EXISTS (SELECT 1 FROM conversation_execution e WHERE e.tenant_id=t.tenant_id AND e.turn_id=t.id AND e.permission_revision=$4 AND e.execution_no=(SELECT max(latest.execution_no) FROM conversation_execution latest WHERE latest.tenant_id=t.tenant_id AND latest.turn_id=t.id))
		ORDER BY t.turn_no DESC,m.created_at DESC LIMIT 6`, tenantID, conversationID, currentTurnID, permissionRevision)
	if err != nil {
		return ""
	}
	defer rows.Close()
	parts := make([]historyMessage, 0, 6)
	for rows.Next() {
		var role, content string
		if rows.Scan(&role, &content) == nil && strings.TrimSpace(content) != "" {
			parts = append(parts, historyMessage{Role: role, Content: content})
		}
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	if rows.Err() != nil {
		return ""
	}
	return boundedHistory(parts)
}

// understandTurn asks the model to route the question and normalises the answer.
// The two post-processing helpers are pure and stay with the routing stage.
func understandTurn(ctx context.Context, client ragv1.RagServiceClient, scope *ragv1.RequestContext,
	query, history string) (*ragv1.UnderstandResponse, *stageFailure) {
	routed, err := client.Understand(ctx, &ragv1.UnderstandRequest{Context: scope, Query: query,
		HistorySummary: history}, grpc.WaitForReady(true))
	if err != nil {
		return nil, grpcStageFailure(ctx, err)
	}
	if routed == nil {
		return nil, &stageFailure{state: "FAILED", code: "invalid_routing_response"}
	}
	routeKnowledgeQuestion(routed, query)
	resolveHistoryReference(routed, query, history)
	return routed, nil
}

// saveAskingCheckpoint persists the ASKING checkpoint before a clarification is
// returned. It returns a stage failure instead of finishing the turn itself.
func saveAskingCheckpoint(ctx context.Context, client ragv1.RagServiceClient, profile string,
	scope *ragv1.RequestContext) *stageFailure {
	if !isV2Profile(profile) {
		return nil
	}
	saved, err := client.SaveCheckpoint(ctx, &ragv1.SaveCheckpointRequest{Context: scope, PendingKind: "ASKING"})
	if err != nil {
		return grpcStageFailure(ctx, err)
	}
	if saved == nil || !saved.Accepted {
		return &stageFailure{state: "FAILED", code: "checkpoint_save_failed"}
	}
	return nil
}

// resolveFactIntent decides which authorized fact tool the turn needs and which
// resource it addresses. It is pure: the decision only reads the routed response
// and the query/history text.
//
// snapshotFallback is true when the model asked for a fact tool but the message
// did not carry a usable identifier; the turn then falls back to knowledge and
// answers with a snapshot qualifier.
func resolveFactIntent(routed *ragv1.UnderstandResponse, query, history string) (factResourceID string, snapshotFallback bool) {
	orderToolsRequired := slices.Contains(routed.RequiredFactTools, "get_order_status") || slices.Contains(routed.RequiredFactTools, "get_logistics")
	productToolsRequired := slices.Contains(routed.RequiredFactTools, "get_stock") || slices.Contains(routed.RequiredFactTools, "get_price")
	if orderToolsRequired && productToolsRequired {
		routed.Intent, routed.Clarification = "clarification", "请分别确认商品 SKU 和订单号后查询。"
		routed.RequiredFactTools = nil
		return "", false
	}
	if orderToolsRequired {
		orderID, valid := routedOrderResource(routed, query)
		if !valid {
			routed.Intent = "clarification"
			routed.Clarification = "请提供有效订单号后再查询订单或物流状态。"
			routed.Confidence = 0.2
			routed.RequiredFactTools = nil
			return "", true
		}
		return orderID, false
	}
	if productToolsRequired {
		sku, valid := routedProductResource(routed, query, history)
		if !valid {
			routed.Intent, routed.Clarification = "clarification", "请确认唯一商品 SKU，再通过授权事实接口查询当前库存或价格；历史库存和商品描述不能代替当前事实。"
			routed.RequiredFactTools = nil
			return "", true
		}
		return sku, false
	}
	return "", false
}

// turnGuard centralises the two authorization checks every stage needs. It writes
// the terminal state through the caller's finish closure.
type turnGuard struct {
	ctx       context.Context
	authority *auth.Authority
	finish    func(state, code, answer string)
}

// authorized refreshes the authorization snapshot; a failure finishes the turn.
func (guard turnGuard) authorized() bool {
	if guard.ctx.Err() != nil {
		state, code := grpcFailure(guard.ctx, guard.ctx.Err())
		guard.finish(state, code, "")
		return false
	}
	if err := guard.authority.Refresh(guard.ctx); err != nil {
		guard.finish("FAILED", auth.FailureCode(err), "")
		return false
	}
	return true
}

// scopeAuthorized also requires the permission revision to be unchanged.
func (guard turnGuard) scopeAuthorized(scope *ragv1.RequestContext) bool {
	if !guard.authorized() {
		return false
	}
	if guard.authority.CurrentRevision() != scope.PermissionRevision {
		guard.finish("FAILED", "permission_changed", "")
		return false
	}
	return true
}

// resumeParentCheckpoint continues an unfinished ASKING checkpoint of the parent
// turn. It is best-effort: any rejection fails the turn with the mapped code.
func resumeParentCheckpoint(ctx context.Context, client ragv1.RagServiceClient, scope *ragv1.RequestContext) *stageFailure {
	resumed, err := client.ResumeCheckpoint(ctx, &ragv1.ResumeCheckpointRequest{Context: scope, PendingKind: "ASKING"}, grpc.WaitForReady(true))
	if err != nil {
		return grpcStageFailure(ctx, err)
	}
	if resumed == nil || !resumed.Accepted {
		return &stageFailure{state: "FAILED", code: "checkpoint_resume_failed"}
	}
	return nil
}

// clarificationDecision and decideClarification are thin wrappers over the pure
// domain rule; the body lives in go/internal/domain/route.go.
type clarificationDecision = domain.ClarificationDecision

func decideClarification(routed *ragv1.UnderstandResponse, snapshotFallback bool) clarificationDecision {
	return domain.DecideClarification(routed, snapshotFallback)
}
