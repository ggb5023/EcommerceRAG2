package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	"github.com/ggb5023/EcommerceRAG/go/internal/ingestclient"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
)

type suggestionRequest struct {
	BusinessDate string `json:"business_date"`
}

type batchSuggestionItem struct {
	VersionID    string `json:"version_id"`
	SuggestionID string `json:"suggestion_id"`
}

type batchApproveRequest struct {
	Items   []batchSuggestionItem `json:"items"`
	Confirm bool                  `json:"confirm"`
	Reason  string                `json:"reason"`
}

func hashJSON(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (g *Handler) adminCreateReviewSuggestion(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpapi.WriteError(w, 404, "not_found", "version not found")
		return
	}
	var in suggestionRequest
	if httpapi.DecodeJSON(r, &in) != nil {
		httpapi.WriteError(w, 400, "invalid_business_date", "business_date is required")
		return
	}
	businessDate, err := parseBusinessDate(strings.TrimSpace(in.BusinessDate))
	if err != nil {
		httpapi.WriteError(w, 400, "invalid_business_date", "business_date must be an ISO date")
		return
	}
	material, err := adminStore.ReviewMaterial(r.Context(), g.DB, g.Session.TenantID, id, scope.shops)
	sourceHash, disclosure := material.SourceHash, material.Disclosure
	meta := material.Meta
	if err != nil {
		httpapi.WriteError(w, 404, "not_found", "version not found")
		return
	}
	rows, err := adminStore.VersionChunks(r.Context(), g.DB, g.Session.TenantID, id)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "review material is unavailable")
		return
	}
	chunks := make([]ReviewChunk, 0)
	evidence := make([]ReviewEvidence, 0)
	var effectiveFrom, effectiveTo *time.Time
	for _, row := range rows {
		chunkID, hash, chunkFrom, chunkTo := row.ChunkID, row.ChunkHash, row.EffectiveFrom, row.EffectiveTo
		chunks = append(chunks, ReviewChunk{ID: chunkID, Hash: hash, DisclosureClass: disclosure})
		evidence = append(evidence, ReviewEvidence{ChunkID: chunkID, Hash: hash})
		if chunkFrom != nil && (effectiveFrom == nil || chunkFrom.Before(*effectiveFrom)) {
			effectiveFrom = chunkFrom
		}
		if chunkTo == nil {
			effectiveTo = nil
		} else if effectiveTo != nil && chunkTo.After(*effectiveTo) {
			effectiveTo = chunkTo
		} else if effectiveTo == nil && chunkFrom != nil {
			effectiveTo = chunkTo
		}
	}
	var authorization ReviewAuthorization
	var metadata map[string]any
	_ = json.Unmarshal(meta, &metadata)
	if raw, ok := metadata["authorization"].(map[string]any); ok {
		authorization.Basis, _ = raw["basis"].(string)
		authorization.Reference, _ = raw["reference"].(string)
		authorization.CheckedAt, _ = raw["checked_at"].(string)
	}
	result := evaluateReviewSuggestion(ReviewSuggestionInput{VersionID: id, ContentHash: sourceHash, BusinessDate: businessDate,
		EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo, DisclosureClass: disclosure, Chunks: chunks,
		Evidence: evidence, Authorization: authorization})
	reasons, _ := json.Marshal(result.Reasons)
	inputHash := hashJSON(map[string]any{"version_id": id, "content_hash": sourceHash, "business_date": in.BusinessDate,
		"chunks": chunks, "authorization": authorization, "rules_version": reviewSuggestionRulesVersion})
	attestation := hashJSON(map[string]any{"input": inputHash, "decision": result.Decision, "reasons": result.Reasons,
		"evidence": result.EvidenceChunkIDs, "model": "local-rules-v1"})
	suggestionID, created, err := adminStore.InsertSuggestion(r.Context(), g.DB, SuggestionRecord{
		TenantID: g.Session.TenantID, VersionID: id, ContentHash: sourceHash, BusinessDate: in.BusinessDate,
		RulesVersion: reviewSuggestionRulesVersion, Decision: result.Decision, Reasons: reasons,
		EvidenceChunks: result.EvidenceChunkIDs, Attestation: attestation, InputHash: inputHash,
	})
	if err == pgx.ErrNoRows {
		suggestionID, created, err = adminStore.SuggestionByHash(r.Context(), g.DB, g.Session.TenantID, id,
			sourceHash, reviewSuggestionRulesVersion)
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "review suggestion is unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{"suggestion_id": strconv.FormatInt(suggestionID, 10), "version_id": strconv.FormatInt(id, 10),
		"content_hash": sourceHash, "decision": result.Decision, "reasons": result.Reasons, "evidence_chunk_ids": result.EvidenceChunkIDs,
		"created_at": created, "reviewed_hash": nil})
}

func (g *Handler) adminBatchApproveSuggestions(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return
	}
	var in batchApproveRequest
	if httpapi.DecodeJSON(r, &in) != nil || !in.Confirm || len(in.Items) == 0 || len(in.Items) > 100 || len(strings.TrimSpace(in.Reason)) < 10 || len(in.Reason) > 2000 {
		httpapi.WriteError(w, 400, "invalid_confirmation", "batch confirmation, items and reason are required")
		return
	}
	type itemResult struct {
		VersionID    string `json:"version_id"`
		SuggestionID string `json:"suggestion_id"`
		OK           bool   `json:"ok"`
		Code         string `json:"code,omitempty"`
		Detail       string `json:"detail,omitempty"`
	}
	results := make([]itemResult, 0, len(in.Items))
	for _, item := range in.Items {
		result := itemResult{VersionID: item.VersionID, SuggestionID: item.SuggestionID}
		versionID, versionErr := strconv.ParseInt(item.VersionID, 10, 64)
		suggestionID, suggestionErr := strconv.ParseInt(item.SuggestionID, 10, 64)
		if versionErr != nil || suggestionErr != nil || versionID < 1 || suggestionID < 1 {
			result.Code = "invalid_reference"
			results = append(results, result)
			continue
		}
		target, err := adminStore.BatchSuggestionTarget(r.Context(), g.DB, g.Session.TenantID, suggestionID, versionID, scope.shops)
		contentHash, decision, sourceHash, versionStatus, reviewStatus, disclosure, fence, shop :=
			target.ContentHash, target.Decision, target.SourceHash, target.VersionStatus, target.ReviewStatus,
			target.Disclosure, target.FencingEpoch, target.ShopID
		if err != nil {
			result.Code = "suggestion_not_found"
			results = append(results, result)
			continue
		}
		if contentHash != sourceHash {
			result.Code, result.Detail = "suggestion_stale", "content_hash no longer matches the version source_hash"
			results = append(results, result)
			continue
		}
		if decision != "approved" {
			result.Code, result.Detail = "suggestion_needs_revision", "suggestion is not approved"
			results = append(results, result)
			continue
		}
		if versionStatus != "ready" || reviewStatus != "pending" {
			result.Code, result.Detail = "version_not_reviewable", "version is not pending/ready"
			results = append(results, result)
			continue
		}
		_, err = g.commandClient().Execute(r.Context(), ingestclient.Actor{Tenant: g.Session.TenantID, User: g.Session.UserID, Shop: shop},
			&ragv1.ExecuteCommandRequest{Action: ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_REVIEW,
				ResourceId: strconv.FormatInt(versionID, 10), ExpectedHash: sourceHash, ExpectedFencingEpoch: fence,
				Reason: strings.TrimSpace(in.Reason), ReviewDecision: "approved", DisclosureClass: disclosure,
				ExternalAllowed: disclosure == "external_allowed"})
		if err != nil {
			result.Code, result.Detail = "review_command_failed", "review command was rejected"
			results = append(results, result)
			continue
		}
		result.OK = true
		results = append(results, result)
	}
	allOK := true
	for _, result := range results {
		if !result.OK {
			allOK = false
		}
	}
	if err := adminStore.RecordBatchAudit(r.Context(), g.DB, g.Session.TenantID, g.Session.UserID,
		strings.TrimSpace(in.Reason), func() string {
			if allOK {
				return "success"
			}
			return "conflict"
		}(), []byte(toJSON(results))); err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "batch audit is unavailable")
		return
	}
	status := "partial"
	if allOK {
		status = "ok"
	}
	httpapi.WriteJSON(w, 200, map[string]any{"status": status, "results": results})
}

func toJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

var _ = context.Background
