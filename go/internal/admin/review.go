package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	rows, err := g.DB.Query(r.Context(), `SELECT v.id,d.logical_key,d.title,s.shop_id,v.source_hash,v.status,v.Disclosure_class,
		v.external_allowed,v.review_status,v.reviewed_hash,v.fencing_epoch,v.chunk_count,v.pipeline_version,v.quality_json,v.created_at,
		j.id,COALESCE(j.stage,''),COALESCE(sug.id,0),COALESCE(sug.decision,''),COALESCE(sug.reasons,'[]'::jsonb),COALESCE(sug.evidence_chunk_ids,'{}'::text[]),COALESCE(sug.created_at,to_timestamp(0))
		FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		LEFT JOIN ingest_job_item i ON i.tenant_id=v.tenant_id AND i.document_version_id=v.id
		LEFT JOIN ingest_job j ON j.tenant_id=i.tenant_id AND j.id=i.job_id
		LEFT JOIN LATERAL (SELECT id,decision,reasons,evidence_chunk_ids,created_at FROM review_suggestion rs
			WHERE rs.tenant_id=v.tenant_id AND rs.version_id=v.id AND rs.content_hash=v.source_hash
			ORDER BY rs.created_at DESC,rs.id DESC LIMIT 1) sug ON true
		WHERE v.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		AND ($3='' OR sug.decision=$3)
		ORDER BY v.created_at DESC LIMIT 300`, g.Session.TenantID, scope.shops, suggestion)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load review queue")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id int64
		var jobID *int64
		var key, title, shop, hash, state, disclosure, reviewState, pipeline, stage string
		var external bool
		var reviewedHash *string
		var fence int64
		var chunks *int32
		var created time.Time
		var quality []byte
		var suggestionID int64
		var suggestionDecision string
		var suggestionReasons []byte
		var suggestionEvidence []string
		var suggestionCreated time.Time
		if rows.Scan(&id, &key, &title, &shop, &hash, &state, &disclosure, &external, &reviewState, &reviewedHash, &fence, &chunks, &pipeline, &quality, &created, &jobID, &stage,
			&suggestionID, &suggestionDecision, &suggestionReasons, &suggestionEvidence, &suggestionCreated) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load review queue")
			return
		}
		item := map[string]any{"version_id": strconv.FormatInt(id, 10), "document_id": key, "title": title, "shop_id": shop,
			"source_hash": hash, "status": state, "disclosure_class": disclosure, "external_allowed": external, "review_status": reviewState,
			"reviewed_hash": reviewedHash, "fencing_epoch": fence, "chunk_count": chunks, "pipeline_version": pipeline,
			"quality": json.RawMessage(quality), "created_at": created,
			"job_id": jobID, "stage": stage}
		if suggestionID != 0 {
			item["suggestion"] = map[string]any{"id": strconv.FormatInt(suggestionID, 10), "decision": suggestionDecision, "reasons": json.RawMessage(suggestionReasons),
				"evidence_chunk_ids": suggestionEvidence, "created_at": suggestionCreated}
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
	var data map[string]any
	err = g.DB.QueryRow(r.Context(), `SELECT jsonb_build_object('version_id',v.id::text,'document_id',d.logical_key,'title',d.title,
		'shop_id',s.shop_id,'source_hash',v.source_hash,'status',v.status,'disclosure_class',v.Disclosure_class,
		'external_allowed',v.external_allowed,'review_status',v.review_status,'reviewed_hash',v.reviewed_hash,
		'fencing_epoch',v.fencing_epoch,'chunk_count',v.chunk_count,'quality',v.quality_json,
		'content',COALESCE((SELECT jsonb_agg(jsonb_build_object('chunk_index',c.chunk_index,'content',c.content,
			'chunk_id','db-chunk-'||c.id::text,'heading_path',c.heading_path,'content_type',c.content_type,
			'split_reason',c.split_reason,'source_position',c.meta_json->'source_position',
			'char_start',c.char_start,'char_end',c.char_end,'warning',c.meta_json->'warning',
			'table_body',c.meta_json->'table_body','table_caption',c.meta_json->'table_caption') ORDER BY c.chunk_index)
			FROM chunk c WHERE c.tenant_id=v.tenant_id AND c.version_id=v.id),'[]'::jsonb),'path',sf.relative_path,'media_type',sf.media_type)
		FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		LEFT JOIN source_file sf ON sf.tenant_id=v.tenant_id AND sf.source_manifest_id=v.source_manifest_id AND sf.document_id=d.logical_key
		WHERE v.tenant_id=$1 AND v.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`,
		g.Session.TenantID, id, scope.shops).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "version not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "review material is unavailable")
		return
	}
	content, err := projectReviewContent(data["content"])
	if err != nil {
		httpapi.WriteError(w, 503, "review_content_unavailable", "review material is unavailable")
		return
	}
	data["content"] = content
	var suggestionJSON []byte
	_ = g.DB.QueryRow(r.Context(), `SELECT jsonb_build_object('decision',decision,'reasons',reasons,
		'evidence_chunk_ids',evidence_chunk_ids,'created_at',created_at)
		FROM review_suggestion WHERE tenant_id=$1 AND version_id=$2 AND content_hash=$3
		ORDER BY created_at DESC,id DESC LIMIT 1`, g.Session.TenantID, id, data["source_hash"]).Scan(&suggestionJSON)
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
