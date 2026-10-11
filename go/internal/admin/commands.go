package admin

import (
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"

	"context"
	"encoding/hex"
	"errors"
	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"net/http"
	"strconv"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/ingestclient"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (g *Handler) commandClient() ingestclient.Client {
	return ingestclient.Client{DB: g.DB, RPC: g.Ingest, Sign: g.issueCommandScope}
}

func (g *Handler) issueCommandScope(ctx context.Context, shop, id, action, hash string) (*ragv1.RequestContext, error) {
	scope, err := g.Authority.IssueScopeForShop(ctx, httpapi.NewRequestID(), shop)
	if err != nil {
		return nil, err
	}
	scope.ScopeAudience, scope.CommandId, scope.CommandAction, scope.CommandPayloadSha256 = "ingest", id, action, hash
	scope.ScopeSignature = hex.EncodeToString(auth.MAC(g.SigningKey, scope))
	return scope, nil
}

func (g *Handler) dispatchCommand(w http.ResponseWriter, r *http.Request, shop string, request *ragv1.ExecuteCommandRequest) {
	request.CommandId = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	out, err := g.commandClient().Execute(r.Context(), ingestclient.Actor{Tenant: g.Session.TenantID, User: g.Session.UserID, Shop: shop}, request)
	if err != nil {
		code := http.StatusServiceUnavailable
		switch status.Code(err) {
		case codes.PermissionDenied:
			code = http.StatusForbidden
		case codes.InvalidArgument:
			code = http.StatusBadRequest
		case codes.AlreadyExists:
			code = http.StatusConflict
		}
		httpapi.WriteError(w, code, "command_failed", "ingestion command failed; use its command identifier to reconcile")
		return
	}
	w.Header().Set("X-Command-Id", out.ID)
	g.writeCommandOutcome(w, out, strings.HasPrefix(ingestclient.Action(request.Action), "version."))
}

func (g *Handler) writeCommandOutcome(w http.ResponseWriter, out ingestclient.Outcome, version bool) {
	result := map[string]any{"command_id": out.ID, "command_status": out.State}
	if out.Result == nil {
		result["status"] = "processing"
		httpapi.WriteJSON(w, http.StatusAccepted, result)
		return
	}
	resource := "job_id"
	if version {
		resource = "version_id"
	}
	result[resource], result["status"], result["review_status"] = out.Result.ResourceId, out.Result.Status, out.Result.ReviewStatus
	result["reviewed_hash"], result["fencing_epoch"], result["idempotent"] = out.Result.ReviewedHash, out.Result.FencingEpoch, out.Result.Idempotent
	result["audit_result"] = "success"
	if out.Result.ErrorCode != "" {
		result["audit_result"], result["code"], result["message"] = "conflict", out.Result.ErrorCode, out.Result.ErrorMessage
		httpapi.WriteJSON(w, http.StatusConflict, result)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func (g *Handler) adminCommandResult(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer); !ok {
		return
	}
	id := r.PathValue("id")
	var shop, action string
	err := g.DB.QueryRow(r.Context(), `SELECT shop_id,action FROM control_command WHERE tenant_id=$1 AND actor_user_id=$2 AND command_id=$3`, g.Session.TenantID, g.Session.UserID, id).Scan(&shop, &action)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "command not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "command lookup is unavailable")
		return
	}
	out, err := g.commandClient().Poll(r.Context(), ingestclient.Actor{Tenant: g.Session.TenantID, User: g.Session.UserID, Shop: shop}, id)
	if err != nil {
		httpapi.WriteError(w, auth.HTTPStatus(err), "command_lookup_failed", "command result is unavailable")
		return
	}
	g.writeCommandOutcome(w, out, strings.HasPrefix(action, "version."))
}

func (g *Handler) adminVersionCommand(w http.ResponseWriter, r *http.Request, action ragv1.IngestCommandAction) {
	scope, ok := g.requireAdmin(w, r, adminRoleReviewer)
	if !ok {
		return
	}
	var in adminConfirmation
	if httpapi.DecodeJSON(r, &in) != nil || !in.Confirm || len(strings.TrimSpace(in.Reason)) < 10 || len(in.Reason) > 2000 || in.ExpectedFencingEpoch < 0 {
		httpapi.WriteError(w, 400, "confirmation_required", "complete the scoped confirmation and reason")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpapi.WriteError(w, 404, "not_found", "version not found")
		return
	}
	shop, key, hash, _, _, _, err := g.lockVersion(r.Context(), id, scope.shops)
	if err != nil {
		g.writeVersionError(w, err)
		return
	}
	if in.TenantID != g.Session.Tenant || in.ShopID != shop || in.DocumentID != key || in.VersionHash != hash || in.PermissionRevision != g.Authority.CurrentRevision() {
		httpapi.WriteError(w, 409, "confirmation_stale", "confirmation does not match the authorized resource")
		return
	}
	g.dispatchCommand(w, r, shop, &ragv1.ExecuteCommandRequest{Action: action, ResourceId: strconv.FormatInt(id, 10), ExpectedHash: in.VersionHash, ExpectedFencingEpoch: in.ExpectedFencingEpoch, Reason: strings.TrimSpace(in.Reason)})
}

func (g *Handler) adminJobCommand(w http.ResponseWriter, r *http.Request, action ragv1.IngestCommandAction) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return
	}
	var in adminJobConfirmation
	if decodeAdminJobConfirmation(w, r, &in) != nil {
		httpapi.WriteError(w, 400, "invalid_confirmation", "invalid job confirmation")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 || !validJobConfirmation(in, *g.Session, id) {
		httpapi.WriteError(w, 409, "confirmation_conflict", "job confirmation changed")
		return
	}
	var shop string
	err = g.DB.QueryRow(r.Context(), `SELECT s.shop_id FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[])`, g.Session.TenantID, id, scope.shops).Scan(&shop)
	if err != nil {
		g.writeVersionError(w, err)
		return
	}
	if shop != in.ShopID {
		httpapi.WriteError(w, 409, "confirmation_conflict", "job shop changed")
		return
	}
	g.dispatchCommand(w, r, shop, &ragv1.ExecuteCommandRequest{Action: action, ResourceId: strconv.FormatInt(id, 10), ExpectedFencingEpoch: in.ExpectedFencingEpoch, Reason: strings.TrimSpace(in.Reason)})
}
