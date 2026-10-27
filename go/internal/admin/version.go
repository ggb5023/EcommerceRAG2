package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

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

func (g *Handler) writeVersionError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "version not found")
	} else {
		httpapi.WriteError(w, 503, "database_unavailable", "version status is unavailable")
	}
}
