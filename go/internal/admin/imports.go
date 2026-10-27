package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	"github.com/ggb5023/EcommerceRAG/go/internal/ingestclient"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
)

type importStagedDetail struct {
	DocumentID             string `json:"document_id"`
	Path                   string `json:"path"`
	Title                  string `json:"title"`
	Format                 string `json:"format"`
	DisclosureClass        string `json:"disclosure_class"`
	EffectiveFrom          string `json:"effective_from"`
	EffectiveTo            string `json:"effective_to"`
	License                string `json:"license"`
	AuthorizationReference string `json:"authorization_reference"`
	CheckedAt              string `json:"checked_at"`
	Snippet                string `json:"snippet"`
	SourceHash             string `json:"source_hash"`
	VersionID              string `json:"version_id"`
}

type importConfirmation struct {
	Confirm bool   `json:"confirm"`
	Reason  string `json:"reason"`
}

func (g *Handler) agentReviewEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := g.DB.QueryRow(ctx, `SELECT enabled FROM tenant_agent_review WHERE tenant_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, g.Session.TenantID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

type agentReviewUpdate struct {
	Enabled       bool   `json:"enabled"`
	PolicyVersion string `json:"policy_version"`
	Confirm       bool   `json:"confirm"`
	Reason        string `json:"reason"`
}

func (g *Handler) adminAgentReviewSetting(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.requireAdmin(w, r, adminRoleMerchant); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		var enabled bool
		var policy, actor string
		var updated time.Time
		err := g.DB.QueryRow(r.Context(), `SELECT t.enabled,t.policy_version,t.created_at,COALESCE(u.external_id,u.role)
			FROM tenant_agent_review t JOIN app_user u ON u.tenant_id=t.tenant_id AND u.id=t.actor_user_id
			WHERE t.tenant_id=$1 ORDER BY t.created_at DESC,t.id DESC LIMIT 1`, g.Session.TenantID).
			Scan(&enabled, &policy, &updated, &actor)
		if errors.Is(err, pgx.ErrNoRows) {
			httpapi.WriteJSON(w, 200, map[string]any{"enabled": false, "policy_version": "", "updated_at": nil, "actor": nil,
				"description": "允许把待审内容发送给模型（默认关闭；只发送片段与规则，不发送顾客对话正文）"})
			return
		}
		if err != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "agent review setting is unavailable")
			return
		}
		httpapi.WriteJSON(w, 200, map[string]any{"enabled": enabled, "policy_version": policy, "updated_at": updated, "actor": actor})
	case http.MethodPut:
		var in agentReviewUpdate
		if httpapi.DecodeJSON(r, &in) != nil || !in.Confirm || len(strings.TrimSpace(in.Reason)) < 10 || len(in.Reason) > 2000 ||
			len(in.PolicyVersion) == 0 || len(in.PolicyVersion) > 64 {
			httpapi.WriteError(w, 400, "invalid_confirmation", "complete the setting confirmation and policy version")
			return
		}
		for _, char := range in.PolicyVersion {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._:-", char) {
				httpapi.WriteError(w, 400, "invalid_policy_version", "policy_version is invalid")
				return
			}
		}
		tx, err := g.DB.Begin(r.Context())
		if err != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "agent review setting is unavailable")
			return
		}
		defer tx.Rollback(r.Context())
		if _, err = tx.Exec(r.Context(), `INSERT INTO tenant_agent_review(tenant_id,enabled,policy_version,actor_user_id)
			VALUES($1,$2,$3,$4)`, g.Session.TenantID, in.Enabled, in.PolicyVersion, g.Session.UserID); err != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "agent review setting is unavailable")
			return
		}
		if err = auditAdmin(r.Context(), tx, g.Session.TenantID, g.Session.UserID, "agent_review.update", "tenant_agent_review",
			"", in.PolicyVersion, "", 0, strings.TrimSpace(in.Reason), "success", map[string]any{"enabled": in.Enabled}); err != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "agent review setting audit is unavailable")
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "agent review setting is unavailable")
			return
		}
		httpapi.WriteJSON(w, 200, map[string]any{"enabled": in.Enabled, "policy_version": in.PolicyVersion})
	default:
		httpapi.WriteError(w, 405, "method_not_allowed", "method is not allowed")
	}
}

func (g *Handler) cleanupExpiredImports(ctx context.Context, tenantID int64, shopID string) {
	rows, err := g.DB.Query(ctx, `SELECT b.id,i.temp_path FROM import_batch b JOIN import_item i ON i.batch_id=b.id
		WHERE b.tenant_id=$1 AND b.shop_id=$2 AND b.status='staged' AND b.expires_at < now()`, tenantID, shopID)
	if err != nil {
		return
	}
	type expired struct {
		id    int64
		paths []string
	}
	batches := map[int64]*expired{}
	for rows.Next() {
		var id int64
		var path string
		if rows.Scan(&id, &path) == nil {
			if batches[id] == nil {
				batches[id] = &expired{id: id}
			}
			batches[id].paths = append(batches[id].paths, path)
		}
	}
	rows.Close()
	for _, batch := range batches {
		if _, err = g.DB.Exec(ctx, `UPDATE import_batch SET status='expired' WHERE tenant_id=$1 AND shop_id=$2 AND id=$3 AND status='staged'`, tenantID, shopID, batch.id); err == nil {
			for _, path := range batch.paths {
				_ = os.Remove(path)
			}
		}
	}
}

func (g *Handler) adminCreateImport(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return
	}
	// R1-R4 are local rules and always run. The tenant switch only gates
	// model-assisted checks, which this version does not call.
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminUpload)
	if err := r.ParseMultipartForm(maxAdminUpload); err != nil {
		httpapi.WriteError(w, 400, "upload_too_large", "manifest package is invalid or too large")
		return
	}
	shopID := strings.TrimSpace(r.FormValue("shop_id"))
	g.cleanupExpiredImports(r.Context(), g.Session.TenantID, shopID)
	if !contains(scope.shops, shopID) {
		httpapi.WriteError(w, 403, "admin_scope_denied", "shop is outside the current identity scope")
		return
	}
	manifest := r.FormValue("manifest")
	if strings.TrimSpace(manifest) == "" || len(manifest) > maxManifestBytes {
		httpapi.WriteError(w, 400, "invalid_manifest", "manifest is required and must be under 256 KiB")
		return
	}
	businessDateText := strings.TrimSpace(r.FormValue("business_date"))
	businessDate, dateErr := parseBusinessDate(businessDateText)
	if dateErr != nil {
		httpapi.WriteError(w, 400, "invalid_business_date", "business_date must be an ISO date")
		return
	}
	paths := r.MultipartForm.Value["paths"]
	fileHeaders := r.MultipartForm.File["files"]
	if len(fileHeaders) == 0 || len(fileHeaders) != len(paths) || len(fileHeaders) > maxImportFiles {
		httpapi.WriteError(w, 400, "invalid_package", "manifest files do not match package paths")
		return
	}
	uploads := make([]UploadedFile, 0, len(fileHeaders))
	for index, header := range fileHeaders {
		if header.Size > maxImportFileSize {
			httpapi.WriteError(w, 413, "file_too_large", "a manifest file exceeds 8 MiB")
			return
		}
		file, openErr := header.Open()
		if openErr != nil {
			httpapi.WriteError(w, 400, "invalid_package", "an uploaded file could not be read")
			return
		}
		temporary, createErr := os.CreateTemp("", "ecr-import-*")
		if createErr != nil {
			_ = file.Close()
			httpapi.WriteError(w, 400, "invalid_package", "an uploaded file could not be read")
			return
		}
		_ = temporary.Chmod(0o600)
		size, copyErr := io.Copy(temporary, io.LimitReader(file, maxImportFileSize+1))
		_ = file.Close()
		closeErr := temporary.Close()
		if copyErr != nil || closeErr != nil || size > maxImportFileSize {
			_ = os.Remove(temporary.Name())
			httpapi.WriteError(w, 413, "file_too_large", "a manifest file exceeds 8 MiB")
			return
		}
		uploads = append(uploads, UploadedFile{TempPath: temporary.Name(), DeclaredName: paths[index], DeclaredSize: header.Size})
	}
	removeUploads := func() {
		for _, upload := range uploads {
			_ = os.Remove(upload.TempPath)
		}
	}
	parsed, parseErr := parseManifest(manifest, uploads)
	if parseErr != nil {
		removeUploads()
		httpapi.WriteError(w, 400, "invalid_package", "manifest package failed validation")
		return
	}
	hashes := make([]string, 0, len(parsed))
	for _, doc := range parsed {
		hashes = append(hashes, doc.SourceHash)
	}
	existing := map[string]bool{}
	rows, queryErr := g.DB.Query(r.Context(), `SELECT DISTINCT v.source_hash FROM document_version v
		JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND s.shop_id=$2 AND v.source_hash=ANY($3)`, g.Session.TenantID, shopID, hashes)
	if queryErr == nil {
		for rows.Next() {
			var hash string
			if rows.Scan(&hash) == nil {
				existing[hash] = true
			}
		}
		rows.Close()
	}
	issues := precheckDocuments(parsed, businessDate, existing)
	manifestDigest := sha256.Sum256([]byte(manifest))
	blocks, warnings := issueCounts(issues)
	tx, err := g.DB.Begin(r.Context())
	if err != nil {
		removeUploads()
		httpapi.WriteError(w, 503, "database_unavailable", "import batch is unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	var batchID int64
	err = tx.QueryRow(r.Context(), `INSERT INTO import_batch(tenant_id,shop_id,created_by,business_date,agent_rules_version,agent_model_id,manifest_yaml,manifest_sha256)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, g.Session.TenantID, shopID, g.Session.UserID,
		businessDateText, importRulesVersion, importAgentModel, manifest, hex.EncodeToString(manifestDigest[:])).Scan(&batchID)
	if err != nil {
		removeUploads()
		httpapi.WriteError(w, 503, "database_unavailable", "import batch is unavailable")
		return
	}
	for index, doc := range parsed {
		detail := importStagedDetail{DocumentID: doc.DocumentID, Path: doc.Path, Title: doc.Title, Format: doc.Format, DisclosureClass: doc.DisclosureClass,
			EffectiveFrom: doc.EffectiveFrom, EffectiveTo: doc.EffectiveTo, License: doc.License,
			AuthorizationReference: doc.AuthorizationReference, CheckedAt: doc.CheckedAt, Snippet: doc.Snippet,
			SourceHash: doc.SourceHash, VersionID: doc.VersionID}
		detailJSON, _ := json.Marshal(detail)
		precheckJSON, _ := json.Marshal(issues[index])
		var effective any
		if doc.EffectiveFrom != "" {
			effective = doc.EffectiveFrom
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO import_item(batch_id,source_hash,temp_path,suggested_disclosure_class,suggested_effective_from,precheck_result,precheck_detail)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, batchID, doc.SourceHash, doc.TempPath, doc.DisclosureClass, effective, precheckJSON, detailJSON); err != nil {
			removeUploads()
			httpapi.WriteError(w, 503, "database_unavailable", "import batch is unavailable")
			return
		}
	}
	if err = auditAdmin(r.Context(), tx, g.Session.TenantID, g.Session.UserID, "import.create", "import_batch",
		strconv.FormatInt(batchID, 10), "", hex.EncodeToString(manifestDigest[:]), 0, "agent precheck v1 created staged batch", "success",
		map[string]any{"rules_version": importRulesVersion, "agent_model_id": importAgentModel, "blocks": blocks, "warnings": warnings}); err != nil {
		removeUploads()
		httpapi.WriteError(w, 503, "database_unavailable", "import batch audit is unavailable")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		removeUploads()
		httpapi.WriteError(w, 503, "database_unavailable", "import batch is unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{"batch_id": strconv.FormatInt(batchID, 10), "status": "staged",
		"item_count": len(parsed), "blocks": blocks, "warnings": warnings, "rules_version": importRulesVersion})
}

func (g *Handler) importBatchScope(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return 0, "", false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpapi.WriteError(w, 404, "not_found", "import batch not found")
		return 0, "", false
	}
	var shop, state string
	var expires time.Time
	err = g.DB.QueryRow(r.Context(), `SELECT shop_id,status,expires_at FROM import_batch WHERE tenant_id=$1 AND id=$2`, g.Session.TenantID, id).Scan(&shop, &state, &expires)
	if errors.Is(err, pgx.ErrNoRows) || !contains(scope.shops, shop) {
		httpapi.WriteError(w, 404, "not_found", "import batch not found")
		return 0, "", false
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
		return 0, "", false
	}
	if state == "staged" && expires.Before(time.Now()) {
		_, _ = g.DB.Exec(r.Context(), `UPDATE import_batch SET status='expired' WHERE tenant_id=$1 AND id=$2 AND status='staged'`, g.Session.TenantID, id)
		httpapi.WriteError(w, 409, "import_expired", "import batch has expired")
		return 0, "", false
	}
	return id, shop, true
}

func (g *Handler) adminImportPreview(w http.ResponseWriter, r *http.Request) {
	id, shop, ok := g.importBatchScope(w, r)
	if !ok {
		return
	}
	var state, manifestHash string
	var businessDate time.Time
	var revokedAt *time.Time
	if err := g.DB.QueryRow(r.Context(), `SELECT status,manifest_sha256,business_date,revoked_at FROM import_batch WHERE tenant_id=$1 AND id=$2`, g.Session.TenantID, id).Scan(&state, &manifestHash, &businessDate, &revokedAt); err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT id,source_hash,suggested_disclosure_class,precheck_result,precheck_detail,
		COALESCE(suggested_effective_from::text,''),temp_path FROM import_item WHERE batch_id=$1 ORDER BY id`, id)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var itemID int64
		var hash, disclosure, effective, tempPath string
		var precheck, detail []byte
		if rows.Scan(&itemID, &hash, &disclosure, &precheck, &detail, &effective, &tempPath) != nil {
			continue
		}
		items = append(items, map[string]any{"item_id": strconv.FormatInt(itemID, 10), "source_hash": hash,
			"suggested_disclosure_class": disclosure, "suggested_effective_from": effective,
			"precheck_result": json.RawMessage(precheck), "precheck_detail": json.RawMessage(detail), "staged": true})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"batch_id": strconv.FormatInt(id, 10), "status": state,
		"shop_id": shop, "business_date": businessDate.Format("2006-01-02"), "manifest_sha256": manifestHash,
		"items": items, "document_created": false, "revoked_at": revokedAt})
}

func (g *Handler) adminConfirmImport(w http.ResponseWriter, r *http.Request) {
	id, shop, ok := g.importBatchScope(w, r)
	if !ok {
		return
	}
	var in importConfirmation
	if httpapi.DecodeJSON(r, &in) != nil || !in.Confirm || len(strings.TrimSpace(in.Reason)) < 10 || len(in.Reason) > 2000 {
		httpapi.WriteError(w, 400, "confirmation_required", "complete the scoped confirmation and reason")
		return
	}
	var state, manifest string
	if err := g.DB.QueryRow(r.Context(), `SELECT status,manifest_yaml FROM import_batch WHERE tenant_id=$1 AND id=$2`, g.Session.TenantID, id).Scan(&state, &manifest); err != nil || state != "staged" {
		httpapi.WriteError(w, 409, "import_state_conflict", "import batch is not staged")
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT temp_path,precheck_detail FROM import_item WHERE batch_id=$1 ORDER BY id`, id)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
		return
	}
	files := make([]UploadedFile, 0)
	documents := make([]ParsedDocument, 0)
	for rows.Next() {
		var tempPath string
		var detailJSON []byte
		if rows.Scan(&tempPath, &detailJSON) != nil {
			continue
		}
		var detail importStagedDetail
		if json.Unmarshal(detailJSON, &detail) != nil {
			rows.Close()
			httpapi.WriteError(w, 409, "import_state_conflict", "import staging is invalid")
			return
		}
		info, statErr := os.Stat(tempPath)
		if statErr != nil {
			rows.Close()
			httpapi.WriteError(w, 409, "import_expired", "import staging has expired")
			return
		}
		files = append(files, UploadedFile{TempPath: tempPath, DeclaredName: detail.Path, DeclaredSize: info.Size()})
		documents = append(documents, ParsedDocument{DocumentID: detail.DocumentID, Title: detail.Title, Format: detail.Format, Path: detail.Path,
			SourceHash: detail.SourceHash, DisclosureClass: detail.DisclosureClass, EffectiveFrom: detail.EffectiveFrom,
			EffectiveTo: detail.EffectiveTo, VersionID: detail.VersionID, TempPath: tempPath})
	}
	rows.Close()
	if len(documents) == 0 {
		httpapi.WriteError(w, 409, "import_state_conflict", "import batch has no staged documents")
		return
	}
	queued, err := g.persistParsed(r.Context(), ParsedPackage{Manifest: manifest, Files: files, Documents: documents}, id, shop)
	if err != nil {
		httpapi.WriteError(w, 503, "ingestion_queue_failed", "package could not be queued")
		return
	}
	_, err = g.DB.Exec(r.Context(), `UPDATE import_batch SET status='confirmed',ingest_job_id=$1 WHERE tenant_id=$2 AND id=$3 AND status='staged'`, queued.JobID, g.Session.TenantID, id)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch confirmation is unavailable")
		return
	}
	_, _ = g.DB.Exec(r.Context(), `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
		VALUES($1,$2,'import.confirm','import_batch',$3,$4,'success',jsonb_build_object('job_id',$5::bigint,'rules_version',$6::text))`,
		g.Session.TenantID, g.Session.UserID, strconv.FormatInt(id, 10), strings.TrimSpace(in.Reason), queued.JobID, importRulesVersion)
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"batch_id": strconv.FormatInt(id, 10), "status": "confirmed",
		"job_id": strconv.FormatInt(queued.JobID, 10), "command_id": strconv.FormatInt(queued.JobID, 10), "item_count": queued.ItemCount})
}

func (g *Handler) adminRevokeImport(w http.ResponseWriter, r *http.Request) {
	id, shop, ok := g.importBatchScope(w, r)
	if !ok {
		return
	}
	var in importConfirmation
	if httpapi.DecodeJSON(r, &in) != nil || !in.Confirm || len(strings.TrimSpace(in.Reason)) < 10 || len(in.Reason) > 2000 {
		httpapi.WriteError(w, 400, "confirmation_required", "complete the scoped confirmation and reason")
		return
	}
	var state string
	if err := g.DB.QueryRow(r.Context(), `SELECT status FROM import_batch WHERE tenant_id=$1 AND id=$2`, g.Session.TenantID, id).Scan(&state); err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
		return
	}
	if state != "staged" && state != "confirmed" {
		httpapi.WriteError(w, 409, "import_state_conflict", "import batch is not revocable")
		return
	}
	if state == "confirmed" {
		type candidate struct {
			id             int64
			status, review string
			hash           string
			fence          int64
		}
		candidates := make([]candidate, 0)
		itemRows, queryErr := g.DB.Query(r.Context(), `SELECT precheck_detail,source_hash FROM import_item WHERE batch_id=$1`, id)
		if queryErr != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
			return
		}
		for itemRows.Next() {
			var detailJSON []byte
			var sourceHash string
			if itemRows.Scan(&detailJSON, &sourceHash) != nil {
				continue
			}
			var detail importStagedDetail
			if json.Unmarshal(detailJSON, &detail) != nil {
				continue
			}
			var versionID, fencing int64
			var versionState, reviewState, versionHash string
			versionErr := g.DB.QueryRow(r.Context(), `SELECT v.id,v.status,v.review_status,v.source_hash,v.fencing_epoch FROM document_version v
				JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
				WHERE v.tenant_id=$1 AND d.logical_key=$2 AND v.source_hash=$3
				ORDER BY v.id DESC LIMIT 1`, g.Session.TenantID, detail.DocumentID, sourceHash).Scan(&versionID, &versionState, &reviewState, &versionHash, &fencing)
			if versionErr != nil {
				// The staged version may still be processing; absence is not a published version.
				continue
			}
			if versionState == "active" || reviewState == "approved" {
				itemRows.Close()
				httpapi.WriteError(w, 409, "import_state_conflict", "published versions cannot be revoked with the batch")
				return
			}
			candidates = append(candidates, candidate{id: versionID, status: versionState, review: reviewState, hash: versionHash, fence: fencing})
		}
		itemRows.Close()
		for _, candidate := range candidates {
			if _, execErr := g.commandClient().Execute(r.Context(), ingestclient.Actor{Tenant: g.Session.TenantID, User: g.Session.UserID, Shop: shop},
				&ragv1.ExecuteCommandRequest{Action: ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_REVOKE,
					ResourceId: strconv.FormatInt(candidate.id, 10), ExpectedHash: candidate.hash,
					ExpectedFencingEpoch: candidate.fence, Reason: strings.TrimSpace(in.Reason)}); execErr != nil {
				httpapi.WriteError(w, 409, "command_failed", "import versions could not be revoked")
				return
			}
		}
	}
	tempRows, _ := g.DB.Query(r.Context(), `SELECT temp_path FROM import_item WHERE batch_id=$1`, id)
	var tempPaths []string
	if tempRows != nil {
		for tempRows.Next() {
			var path string
			if tempRows.Scan(&path) == nil {
				tempPaths = append(tempPaths, path)
			}
		}
		tempRows.Close()
	}
	if _, err := g.DB.Exec(r.Context(), `UPDATE import_batch SET status='revoked',revoked_at=now() WHERE tenant_id=$1 AND id=$2`, g.Session.TenantID, id); err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch revoke is unavailable")
		return
	}
	for _, path := range tempPaths {
		_ = os.Remove(path)
	}
	_, _ = g.DB.Exec(r.Context(), `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result,metadata_json)
		VALUES($1,$2,'import.revoke','import_batch',$3,$4,'success',jsonb_build_object('rules_version',$5::text))`,
		g.Session.TenantID, g.Session.UserID, strconv.FormatInt(id, 10), strings.TrimSpace(in.Reason), importRulesVersion)
	httpapi.WriteJSON(w, 200, map[string]any{"batch_id": strconv.FormatInt(id, 10), "status": "revoked", "shop_id": shop})
}
