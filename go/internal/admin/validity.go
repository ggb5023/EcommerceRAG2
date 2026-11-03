package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	"github.com/ggb5023/EcommerceRAG/go/internal/ingestclient"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

type validityInput struct {
	Confirm              bool   `json:"confirm"`
	EffectiveFrom        string `json:"effective_from"`
	EffectiveTo          string `json:"effective_to"`
	ExpectedFencingEpoch int64  `json:"expected_fencing_epoch"`
	PermissionRevision   string `json:"permission_revision"`
	Reason               string `json:"reason"`
}

func validityDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", err
	}
	return parsed.Format("2006-01-02"), nil
}

func validValidityDates(from, to string) bool {
	from, err := validityDate(from)
	if err != nil {
		return false
	}
	to, err = validityDate(to)
	if err != nil {
		return false
	}
	if from != "" && to != "" {
		fromTime, _ := time.Parse("2006-01-02", from)
		toTime, _ := time.Parse("2006-01-02", to)
		return toTime.After(fromTime)
	}
	return true
}

func (g *Handler) adminValidityVersion(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return
	}
	var in validityInput
	if httpapi.DecodeJSON(r, &in) != nil || !in.Confirm || len(strings.TrimSpace(in.Reason)) < 10 || len(in.Reason) > 2000 || in.ExpectedFencingEpoch < 0 || !validValidityDates(in.EffectiveFrom, in.EffectiveTo) {
		httpapi.WriteError(w, 400, "invalid_validity", "valid dates, confirmation and reason are required")
		return
	}
	if in.PermissionRevision != g.Authority.CurrentRevision() {
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
	from, _ := validityDate(in.EffectiveFrom)
	to, _ := validityDate(in.EffectiveTo)
	request := &ragv1.ExecuteCommandRequest{
		Action:               ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_VALIDITY,
		ResourceId:           strconv.FormatInt(id, 10),
		ExpectedHash:         hash,
		ExpectedFencingEpoch: in.ExpectedFencingEpoch,
		Reason:               strings.TrimSpace(in.Reason),
		EffectiveFrom:        from,
		EffectiveTo:          to,
	}
	request.CommandId = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	out, err := g.commandClient().Execute(r.Context(), ingestActor(g, shop), request)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "command_failed", "validity command failed; use its command identifier to reconcile")
		return
	}
	result := "success"
	if out.Result != nil && out.Result.ErrorCode != "" {
		result = "conflict"
	}
	if err := g.auditValidity(r.Context(), id, hash, from, to, in.ExpectedFencingEpoch, in.Reason, result); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "audit_unavailable", "validity audit is unavailable")
		return
	}
	w.Header().Set("X-Command-Id", out.ID)
	g.writeCommandOutcome(w, out, true)
}

func ingestActor(g *Handler, shop string) ingestclient.Actor {
	return ingestclient.Actor{Tenant: g.Session.TenantID, User: g.Session.UserID, Shop: shop}
}

func (g *Handler) auditValidity(ctx context.Context, id int64, hash, from, to string, fence int64, reason, result string) error {
	tx, err := g.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = auditAdmin(ctx, tx, g.Session.TenantID, g.Session.UserID, "version.validity", "document_version",
		strconv.FormatInt(id, 10), "", hash, fence, reason, result,
		map[string]any{"effective_from": from, "effective_to": to}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
