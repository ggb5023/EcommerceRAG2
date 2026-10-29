package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
)

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
	rows, err := adminStore.Versions(r.Context(), g.DB, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load version history")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"version_id": strconv.FormatInt(row.VersionID, 10),
			"document_id": row.DocumentID, "title": row.Title, "shop_id": row.ShopID, "status": row.Status,
			"review_status": row.ReviewStatus, "source_hash": row.SourceHash, "reviewed_hash": row.ReviewedHash,
			"disclosure_class": row.DisclosureClass, "external_allowed": row.ExternalAllowed,
			"fencing_epoch": row.FencingEpoch, "chunk_count": row.ChunkCount, "created_at": row.CreatedAt,
			"activated_at": row.ActivatedAt})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) lockVersion(ctx context.Context, id int64, shops []string) (shop, key, hash string, fence int64, state, review string, err error) {
	var scopeRow VersionScopeRow
	scopeRow, err = adminStore.VersionScope(ctx, g.DB, g.Session.TenantID, id, shops)
	shop, key, hash, fence, state, review = scopeRow.ShopID, scopeRow.DocumentID, scopeRow.SourceHash,
		scopeRow.FencingEpoch, scopeRow.Status, scopeRow.ReviewStatus
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

func (g *Handler) writeVersionError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "version not found")
	} else {
		httpapi.WriteError(w, 503, "database_unavailable", "version status is unavailable")
	}
}
