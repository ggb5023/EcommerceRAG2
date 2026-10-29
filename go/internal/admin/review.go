package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
)

func (g *Handler) adminReviews(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleReviewer)
	if !ok {
		return
	}
	suggestion := strings.TrimSpace(r.URL.Query().Get("suggestion"))
	if suggestion != "" && suggestion != "needs_revision" && suggestion != "approved" {
		httpapi.WriteError(w, 400, "invalid_suggestion_filter", "suggestion filter is invalid")
		return
	}
	queue, err := adminStore.ReviewQueue(r.Context(), g.DB, g.Session.TenantID, scope.shops, suggestion)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load review queue")
		return
	}
	items := make([]map[string]any, 0, len(queue))
	for _, row := range queue {
		item := map[string]any{"version_id": strconv.FormatInt(row.VersionID, 10), "document_id": row.DocumentID,
			"title": row.Title, "shop_id": row.ShopID, "source_hash": row.SourceHash, "status": row.Status,
			"disclosure_class": row.DisclosureClass, "external_allowed": row.ExternalAllowed,
			"review_status": row.ReviewStatus, "reviewed_hash": row.ReviewedHash, "fencing_epoch": row.FencingEpoch,
			"chunk_count": row.ChunkCount, "pipeline_version": row.PipelineVersion,
			"quality": json.RawMessage(row.Quality), "created_at": row.CreatedAt,
			"job_id": row.JobID, "stage": row.Stage}
		if row.SuggestionID != 0 {
			item["suggestion"] = map[string]any{"id": strconv.FormatInt(row.SuggestionID, 10),
				"decision": row.SuggestionDecision, "reasons": json.RawMessage(row.SuggestionReasons),
				"evidence_chunk_ids": row.SuggestionEvidence, "created_at": row.SuggestionCreated}
		}
		items = append(items, item)
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
	raw, err := adminStore.ReviewDetail(r.Context(), g.DB, g.Session.TenantID, id, scope.shops)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "version not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "review material is unavailable")
		return
	}
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		httpapi.WriteError(w, 503, "review_content_unavailable", "review material is unavailable")
		return
	}
	content, err := projectReviewContent(data["content"])
	if err != nil {
		httpapi.WriteError(w, 503, "review_content_unavailable", "review material is unavailable")
		return
	}
	data["content"] = content
	suggestionJSON, _ := adminStore.LatestSuggestion(r.Context(), g.DB, g.Session.TenantID, id,
		strings.TrimSpace(data["source_hash"].(string)))
	if suggestionJSON != nil {
		data["suggestion"] = json.RawMessage(suggestionJSON)
	}
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
