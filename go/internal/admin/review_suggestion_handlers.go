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
	var sourceHash, disclosure, versionStatus, reviewStatus, shop string
	var meta []byte
	err = g.DB.QueryRow(r.Context(), `SELECT v.source_hash,v.disclosure_class,v.status,v.review_status,
		d.meta_json,s.shop_id FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND v.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`,
		g.Session.TenantID, id, scope.shops).Scan(&sourceHash, &disclosure, &versionStatus, &reviewStatus, &meta, &shop)
	if err != nil {
		httpapi.WriteError(w, 404, "not_found", "version not found")
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT 'db-chunk-'||id,chunk_hash,effective_from,effective_to FROM chunk WHERE tenant_id=$1 AND version_id=$2 ORDER BY chunk_index`, g.Session.TenantID, id)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "review material is unavailable")
		return
	}
	chunks := make([]ReviewChunk, 0)
	evidence := make([]ReviewEvidence, 0)
	var effectiveFrom, effectiveTo *time.Time
	for rows.Next() {
		var chunkID, hash string
		var chunkFrom, chunkTo *time.Time
		if rows.Scan(&chunkID, &hash, &chunkFrom, &chunkTo) == nil {
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
	}
	rows.Close()
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
	var suggestionID int64
	var created time.Time
	err = g.DB.QueryRow(r.Context(), `INSERT INTO review_suggestion(tenant_id,version_id,content_hash,business_date,
		agent_rules_version,decision,reasons,evidence_chunk_ids,agent_model_id,agent_attestation_sha256,agent_input_sha256)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'local-rules-v1',$9,$10)
		ON CONFLICT (tenant_id,version_id,content_hash,agent_rules_version) DO NOTHING
		RETURNING id,created_at`, g.Session.TenantID, id, sourceHash, in.BusinessDate, reviewSuggestionRulesVersion,
		result.Decision, reasons, result.EvidenceChunkIDs, attestation, inputHash).Scan(&suggestionID, &created)
	if err == pgx.ErrNoRows {
		err = g.DB.QueryRow(r.Context(), `SELECT id,created_at FROM review_suggestion WHERE tenant_id=$1 AND version_id=$2
			AND content_hash=$3 AND agent_rules_version=$4`, g.Session.TenantID, id, sourceHash, reviewSuggestionRulesVersion).Scan(&suggestionID, &created)
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
		var contentHash, decision, sourceHash, versionStatus, reviewStatus, disclosure, shop string
		var fence int64
		err := g.DB.QueryRow(r.Context(), `SELECT rs.content_hash,rs.decision,v.source_hash,v.status,v.review_status,v.disclosure_class,v.fencing_epoch,s.shop_id
			FROM review_suggestion rs JOIN document_version v ON v.tenant_id=rs.tenant_id AND v.id=rs.version_id
			JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
			JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
			WHERE rs.tenant_id=$1 AND rs.id=$2 AND rs.version_id=$3 AND s.shop_id=ANY($4::text[])`,
			g.Session.TenantID, suggestionID, versionID, scope.shops).Scan(&contentHash, &decision, &sourceHash, &versionStatus, &reviewStatus, &disclosure, &fence, &shop)
		if err != nil {
			result.Code = "suggestion_not_found"
			results = append(results, result)
			continue
		}
		if contentHash != sourceHash {
			result.Code = "suggestion_stale"
			results = append(results, result)
			continue
		}
		if decision != "approved" {
			result.Code = "suggestion_needs_revision"
			results = append(results, result)
			continue
		}
		if versionStatus != "ready" || reviewStatus != "pending" {
			result.Code = "version_not_reviewable"
			results = append(results, result)
			continue
		}
		_, err = g.commandClient().Execute(r.Context(), ingestclient.Actor{Tenant: g.Session.TenantID, User: g.Session.UserID, Shop: shop},
			&ragv1.ExecuteCommandRequest{Action: ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_REVIEW,
				ResourceId: strconv.FormatInt(versionID, 10), ExpectedHash: sourceHash, ExpectedFencingEpoch: fence,
				Reason: strings.TrimSpace(in.Reason), ReviewDecision: "approved", DisclosureClass: disclosure,
				ExternalAllowed: disclosure == "external_allowed"})
		if err != nil {
			result.Code = "review_command_failed"
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
	if _, err := g.DB.Exec(r.Context(), `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
		VALUES($1,$2,'review_suggestion.batch_approve','review_suggestion',$3,$4,$5,jsonb_build_object('items',$6::jsonb))`,
		g.Session.TenantID, g.Session.UserID, "batch", strings.TrimSpace(in.Reason), func() string {
			if allOK {
				return "success"
			}
			return "conflict"
		}(), toJSON(results)); err != nil {
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
