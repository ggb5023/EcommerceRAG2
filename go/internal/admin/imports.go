package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
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
	enabled, err := adminStore.AgentReviewEnabled(ctx, g.DB, g.Session.TenantID)
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
		setting, err := adminStore.LatestAgentReviewSetting(r.Context(), g.DB, g.Session.TenantID)
		enabled, policy, updated, actor := setting.Enabled, setting.Policy, setting.Updated, setting.Actor
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
		if err = adminStore.WriteAgentReviewSetting(r.Context(), tx, AgentReviewSetting{
			TenantID: g.Session.TenantID, Enabled: in.Enabled, PolicyVersion: in.PolicyVersion, ActorUserID: g.Session.UserID,
		}); err != nil {
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
	batches, err := adminStore.ExpiredStagedBatches(ctx, g.DB, tenantID, shopID)
	if err != nil {
		return
	}
	for batchID, paths := range batches {
		if err = adminStore.MarkBatchExpiredForShop(ctx, g.DB, tenantID, shopID, batchID); err == nil {
			for _, path := range paths {
				_ = os.Remove(path)
			}
		}
	}
	if _, err = adminStore.ReclaimTimedOutConfirms(ctx, g.DB, tenantID, 0); err != nil {
		return
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
	existing := adminStore.ExistingSourceHashes(r.Context(), g.DB, g.Session.TenantID, shopID, hashes)
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
	batchID, err := adminStore.InsertImportBatch(r.Context(), tx, ImportBatchRecord{
		TenantID: g.Session.TenantID, ShopID: shopID, CreatedBy: g.Session.UserID, BusinessDate: businessDateText,
		RulesVersion: importRulesVersion, AgentModelID: importAgentModel, ManifestYAML: manifest,
		ManifestHash: hex.EncodeToString(manifestDigest[:]),
	})
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
		if err = adminStore.InsertImportItem(r.Context(), tx, batchID, doc.SourceHash, doc.TempPath, doc.DisclosureClass,
			effective, precheckJSON, detailJSON); err != nil {
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
	scopeRow, err := adminStore.ImportBatchScope(r.Context(), g.DB, g.Session.TenantID, id)
	shop, state, expires := scopeRow.ShopID, scopeRow.Status, scopeRow.ExpiresAt
	if errors.Is(err, pgx.ErrNoRows) || !contains(scope.shops, shop) {
		httpapi.WriteError(w, 404, "not_found", "import batch not found")
		return 0, "", false
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
		return 0, "", false
	}
	if state == "staged" && expires.Before(time.Now()) {
		_ = adminStore.MarkBatchExpired(r.Context(), g.DB, g.Session.TenantID, id)
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
	meta, err := adminStore.BatchPreviewMetadata(r.Context(), g.DB, g.Session.TenantID, id)
	state, manifestHash, businessDate, revokedAt := meta.Status, meta.ManifestHash, meta.BusinessDate, meta.RevokedAt
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
		return
	}
	rows, err := adminStore.BatchItems(r.Context(), g.DB, id)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"item_id": strconv.FormatInt(row.ID, 10), "source_hash": row.SourceHash,
			"suggested_disclosure_class": row.Disclosure, "suggested_effective_from": row.EffectiveFrom,
			"precheck_result": json.RawMessage(row.Precheck), "precheck_detail": json.RawMessage(row.Detail), "staged": true})
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
	state, manifest, confirmErr := adminStore.BatchConfirmState(r.Context(), g.DB, g.Session.TenantID, id)
	if confirmErr != nil || state != "staged" {
		httpapi.WriteError(w, 409, "import_state_conflict", "import batch is not staged")
		return
	}
	staged, err := adminStore.BatchStagedFiles(r.Context(), g.DB, id)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
		return
	}
	files := make([]UploadedFile, 0)
	documents := make([]ParsedDocument, 0)
	for _, stagedFile := range staged {
		tempPath, detailJSON := stagedFile.TempPath, stagedFile.Detail
		var detail importStagedDetail
		if json.Unmarshal(detailJSON, &detail) != nil {
			httpapi.WriteError(w, 409, "import_state_conflict", "import staging is invalid")
			return
		}
		info, statErr := os.Stat(tempPath)
		if statErr != nil {
			httpapi.WriteError(w, 409, "import_expired", "import staging has expired")
			return
		}
		files = append(files, UploadedFile{TempPath: tempPath, DeclaredName: detail.Path, DeclaredSize: info.Size()})
		documents = append(documents, ParsedDocument{DocumentID: detail.DocumentID, Title: detail.Title, Format: detail.Format, Path: detail.Path,
			SourceHash: detail.SourceHash, DisclosureClass: detail.DisclosureClass, EffectiveFrom: detail.EffectiveFrom,
			EffectiveTo: detail.EffectiveTo, VersionID: detail.VersionID, TempPath: tempPath})
	}
	if len(documents) == 0 {
		httpapi.WriteError(w, 409, "import_state_conflict", "import batch has no staged documents")
		return
	}
	claimed, err := adminStore.ClaimBatchForConfirm(r.Context(), g.DB, g.Session.TenantID, shop, id)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch confirmation is unavailable")
		return
	}
	if claimed == 0 {
		httpapi.WriteError(w, 409, "batch_already_confirming", "import batch is already being confirmed")
		return
	}
	queued, err := g.persistParsed(r.Context(), ParsedPackage{Manifest: manifest, Files: files, Documents: documents}, id, shop)
	if err != nil {
		if settleErr := adminStore.ReleaseConfirmClaim(r.Context(), g.DB, g.Session.TenantID, g.Session.UserID, id,
			queueCauseOf(err), importRulesVersion); settleErr != nil {
			log.Printf("confirm failure settlement failed")
		}
		// Non-retryable causes keep their own code (403/409/500); a transient queue
		// failure is reported as confirm_queue_failed so the caller can retry.
		statusCode, code, message, cause := queueFailureResponse(err)
		if code == "ingest_queue_failed" {
			code = "confirm_queue_failed"
		}
		httpapi.WriteErrorCause(w, statusCode, code, message, cause)
		return
	}
	settled, err := adminStore.SettleConfirmSuccess(r.Context(), g.DB, queued.JobID, g.Session.TenantID, id)
	if err != nil || settled == 0 {
		httpapi.WriteError(w, 500, "confirm_settle_failed", "import batch confirmation could not be settled")
		return
	}
	_ = adminStore.RecordImportConfirmAudit(r.Context(), g.DB, g.Session.TenantID, g.Session.UserID, id,
		strings.TrimSpace(in.Reason), queued.JobID, importRulesVersion)
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
	state, err := adminStore.BatchStatus(r.Context(), g.DB, g.Session.TenantID, id)
	if err != nil {
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
		itemRows, queryErr := adminStore.BatchRevokeItems(r.Context(), g.DB, id)
		if queryErr != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "import batch lookup is unavailable")
			return
		}
		for _, itemRow := range itemRows {
			var detail importStagedDetail
			if json.Unmarshal(itemRow.Detail, &detail) != nil {
				continue
			}
			version, versionErr := adminStore.VersionForRevoke(r.Context(), g.DB, g.Session.TenantID, detail.DocumentID, itemRow.SourceHash)
			if versionErr != nil {
				// The staged version may still be processing; absence is not a published version.
				continue
			}
			if version.Status == "active" {
				httpapi.WriteError(w, 409, "import_state_conflict", "published versions cannot be revoked with the batch")
				return
			}
			candidates = append(candidates, candidate{id: version.ID, status: version.Status, review: version.ReviewStatus, hash: version.SourceHash, fence: version.FencingEpoch})
		}
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
	tempPaths := adminStore.BatchTempPaths(r.Context(), g.DB, id)
	if err := adminStore.MarkBatchRevoked(r.Context(), g.DB, g.Session.TenantID, id); err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "import batch revoke is unavailable")
		return
	}
	for _, path := range tempPaths {
		_ = os.Remove(path)
	}
	_ = adminStore.RecordImportRevokeAudit(r.Context(), g.DB, g.Session.TenantID, g.Session.UserID, id,
		strings.TrimSpace(in.Reason), importRulesVersion)
	httpapi.WriteJSON(w, 200, map[string]any{"batch_id": strconv.FormatInt(id, 10), "status": "revoked", "shop_id": shop})
}
