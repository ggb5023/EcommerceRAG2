package admin

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type adminConflictError struct{ message string }

func (g *Handler) adminSources(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleObserver)
	if !ok {
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT s.id,s.shop_id,s.type,CASE WHEN s.type='admin_manifest' THEN s.uri ELSE NULL END,s.created_at,
		(SELECT count(*) FROM document d WHERE d.tenant_id=s.tenant_id AND d.source_id=s.id) AS documents,
		(SELECT max(sm.created_at) FROM source_manifest sm WHERE sm.tenant_id=s.tenant_id AND sm.source_id=s.id) AS latest_import
		FROM source s WHERE s.tenant_id=$1 AND s.shop_id=ANY($2::text[])
		AND s.type<>'synthetic-acl' ORDER BY s.created_at DESC LIMIT 300`, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load sources")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, docs int64
		var shop, kind string
		var uri *string
		var created time.Time
		var latest *time.Time
		if rows.Scan(&id, &shop, &kind, &uri, &created, &docs, &latest) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load sources")
			return
		}
		items = append(items, map[string]any{"source_id": strconv.FormatInt(id, 10), "shop_id": shop,
			"type": kind, "uri": uri, "documents": docs, "created_at": created, "latest_import": latest})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminImportPackage(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminUpload)
	if err := r.ParseMultipartForm(maxAdminUpload); err != nil {
		httpapi.WriteError(w, 400, "upload_too_large", "manifest package is invalid or too large")
		return
	}
	shopID := strings.TrimSpace(r.FormValue("shop_id"))
	if !contains(scope.shops, shopID) {
		httpapi.WriteError(w, 403, "admin_scope_denied", "shop is outside the current identity scope")
		return
	}
	manifest := r.FormValue("manifest")
	if strings.TrimSpace(manifest) == "" || len(manifest) > 256*1024 {
		httpapi.WriteError(w, 400, "invalid_manifest", "manifest is required and must be under 256 KiB")
		return
	}
	paths := r.MultipartForm.Value["paths"]
	fileHeaders := r.MultipartForm.File["files"]
	if len(fileHeaders) == 0 || len(fileHeaders) != len(paths) || len(fileHeaders) > 100 {
		httpapi.WriteError(w, 400, "invalid_package", "manifest files do not match package paths")
		return
	}
	files := make([]*ragv1.PackageFile, 0, len(fileHeaders))
	for index, header := range fileHeaders {
		if header.Size > 8*1024*1024 {
			httpapi.WriteError(w, 413, "file_too_large", "a manifest file exceeds 8 MiB")
			return
		}
		file, err := header.Open()
		if err != nil {
			httpapi.WriteError(w, 400, "invalid_package", "an uploaded file could not be read")
			return
		}
		content, readErr := io.ReadAll(io.LimitReader(file, 8*1024*1024+1))
		_ = file.Close()
		if readErr != nil || len(content) > 8*1024*1024 {
			httpapi.WriteError(w, 413, "file_too_large", "a manifest file exceeds 8 MiB")
			return
		}
		files = append(files, &ragv1.PackageFile{Path: paths[index], Content: content})
	}
	queued, err := g.queuePackage(r.Context(), manifest, files, g.Session.TenantID, g.Session.UserID, shopID)
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			httpapi.WriteError(w, 400, "invalid_package", "manifest package failed validation")
			return
		}
		if status.Code(err) == codes.PermissionDenied {
			httpapi.WriteError(w, 403, "admin_scope_denied", "submitter authorization changed")
			return
		}
		if status.Code(err) == codes.AlreadyExists {
			httpapi.WriteError(w, 409, "version_conflict", "idempotency key was reused with a different package")
			return
		}
		var conflict *adminConflictError
		if errors.As(err, &conflict) {
			httpapi.WriteError(w, 409, "version_conflict", conflict.Error())
			return
		}
		httpapi.WriteError(w, 503, "ingestion_queue_failed", "package could not be queued")
		return
	}
	// The authorized Submit transaction creates the source and advances the
	// resource permission revision itself. Return its receipt instead of calling
	// that successful write a 403. Subsequent operations need a fresh session.
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"job_id": strconv.FormatInt(queued.JobID, 10),
		"manifest_sha256": queued.ManifestSHA256, "package_sha256": queued.PackageSHA256,
		"item_count": queued.ItemCount, "status": queued.Status, "is_mock": httpapi.IsMockProfile(g.Profile)})
}

func (g *Handler) adminJobs(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleObserver)
	if !ok {
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT j.id,j.stage,j.status,s.shop_id,j.manifest_sha256,j.dataset_sha256,
		j.fencing_epoch,j.cancel_requested,j.created_at,j.updated_at,count(i.id),
		count(i.id) FILTER (WHERE i.status='pending'),count(i.id) FILTER (WHERE i.status='running'),
		count(i.id) FILTER (WHERE i.status='awaiting_review'),count(i.id) FILTER (WHERE i.status='done'),
		count(i.id) FILTER (WHERE i.status='failed') FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		LEFT JOIN ingest_job_item i ON i.tenant_id=j.tenant_id AND i.job_id=j.id
		WHERE j.tenant_id=$1 AND s.shop_id=ANY($2::text[]) AND s.type='admin_manifest'
		GROUP BY j.id,s.shop_id ORDER BY j.created_at DESC LIMIT 200`, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion jobs")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, fence, all, pending, running, review, done, failed int64
		var stage, state, shop, manifestHash string
		var datasetHash *string
		var cancelRequested bool
		var created, updated time.Time
		if rows.Scan(&id, &stage, &state, &shop, &manifestHash, &datasetHash, &fence, &cancelRequested,
			&created, &updated, &all, &pending, &running, &review, &done, &failed) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion jobs")
			return
		}
		items = append(items, map[string]any{"job_id": strconv.FormatInt(id, 10), "stage": stage, "status": state,
			"shop_id": shop, "manifest_sha256": manifestHash, "dataset_sha256": datasetHash,
			"fencing_epoch": fence, "cancel_requested": cancelRequested, "item_count": all,
			"pending": pending, "running": running, "awaiting_review": review, "done": done, "failed": failed,
			"created_at": created, "updated_at": updated})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": items})
}

func (g *Handler) adminJobDetail(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleObserver)
	if !ok {
		return
	}
	jobID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, 404, "not_found", "ingestion job not found")
		return
	}
	var jobStatus, jobStage, shopID, manifestHash string
	var datasetHash, errorCode *string
	var fencingEpoch int64
	var cancelRequested bool
	var leaseExpiresAt *time.Time
	err = g.DB.QueryRow(r.Context(), `SELECT j.status,j.stage,s.shop_id,j.manifest_sha256,j.dataset_sha256,
		j.fencing_epoch,j.cancel_requested,j.lease_expires_at,j.error_code FROM ingest_job j
		JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'`,
		g.Session.TenantID, jobID, scope.shops).Scan(&jobStatus, &jobStage, &shopID, &manifestHash,
		&datasetHash, &fencingEpoch, &cancelRequested, &leaseExpiresAt, &errorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "ingestion job not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion job")
		return
	}
	rows, err := g.DB.Query(r.Context(), `SELECT i.id,i.data_id,i.payload_hash,i.status,i.stage,i.retry_count,
		i.error_code,i.cancel_requested,i.fencing_epoch,i.document_version_id::text,v.status,v.review_status,v.source_hash
		FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
		JOIN ingest_job_item i ON i.tenant_id=j.tenant_id AND i.job_id=j.id
		LEFT JOIN document_version v ON v.tenant_id=i.tenant_id AND v.id=i.document_version_id
		WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest'
		ORDER BY i.id LIMIT 500`, g.Session.TenantID, jobID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion items")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, retries, fence int64
		var dataID, payloadHash, state, stage string
		var errorCode *string
		var cancelled bool
		var versionID, versionState, reviewState, versionHash *string
		if rows.Scan(&id, &dataID, &payloadHash, &state, &stage, &retries, &errorCode, &cancelled,
			&fence, &versionID, &versionState, &reviewState, &versionHash) != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion items")
			return
		}
		items = append(items, map[string]any{"item_id": strconv.FormatInt(id, 10), "document_id": dataID,
			"payload_hash": payloadHash, "status": state, "stage": stage, "retry_count": retries,
			"error_code": errorCode, "cancel_requested": cancelled, "fencing_epoch": fence,
			"version_id": versionID, "version_status": versionState, "review_status": reviewState,
			"version_hash": versionHash})
	}
	if err = rows.Err(); err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion items")
		return
	}
	if len(items) == 0 {
		var exists bool
		if err = g.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM ingest_job j JOIN source s ON s.tenant_id=j.tenant_id AND s.id=j.source_id
			WHERE j.tenant_id=$1 AND j.id=$2 AND s.shop_id=ANY($3::text[]) AND s.type='admin_manifest')`,
			g.Session.TenantID, jobID, scope.shops).Scan(&exists); err != nil {
			httpapi.WriteError(w, 503, "database_unavailable", "could not verify ingestion job")
			return
		}
		if !exists {
			httpapi.WriteError(w, 404, "not_found", "ingestion job not found")
			return
		}
	}
	httpapi.WriteJSON(w, 200, map[string]any{"job_id": strconv.FormatInt(jobID, 10), "status": jobStatus,
		"stage": jobStage, "shop_id": shopID, "manifest_sha256": manifestHash, "dataset_sha256": datasetHash,
		"fencing_epoch": fencingEpoch, "cancel_requested": cancelRequested, "lease_expires_at": leaseExpiresAt,
		"error_code": errorCode, "items": items})
}

func (e *adminConflictError) Error() string { return e.message }
