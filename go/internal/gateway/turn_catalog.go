package gateway

import (
	"context"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

// applyCatalogFilter extracts explicit catalog constraints, verifies that an
// active reviewed product row exists, and puts the signed filter on the scope.
// A missing row becomes a clarification and never reaches the model.
func applyCatalogFilter(ctx context.Context, db *pgxpool.Pool, tenantID int64,
	authority *auth.Authority, base *ragv1.RequestContext,
	routed *ragv1.UnderstandResponse, query string) *stageFailure {
	if len(routed.RequiredFactTools) > 0 {
		return nil
	}
	filter := domain.CatalogFilterCandidates(routed, query)
	if filter == nil {
		return nil
	}
	matches, err := catalogFilterMatches(ctx, db, tenantID, base.ShopId, base.BusinessDate, filter)
	if err != nil {
		return &stageFailure{state: "FAILED", code: "catalog_filter_unavailable"}
	}
	if !matches {
		routed.Intent = "clarification"
		routed.Clarification = "没有找到匹配当前 SKU 或规格的已发布商品，请确认编码或规格后重试。"
		routed.RequiredFactTools = nil
		routed.Entities = nil
		routed.Confidence = 0.2
		return nil
	}
	if err := authority.ApplyCatalogFilter(ctx, base, filter); err != nil {
		return &stageFailure{state: "FAILED", code: auth.FailureCode(err)}
	}
	return nil
}
