package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errImportUpload = errors.New("uploaded file could not be read")

type adminConflictError struct{ message string }

func (g *Handler) adminSources(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleObserver)
	if !ok {
		return
	}
	sources, err := adminStore.Sources(r.Context(), g.DB, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load sources")
		return
	}
	items := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		items = append(items, map[string]any{"source_id": strconv.FormatInt(source.ID, 10), "shop_id": source.ShopID,
			"type": source.Type, "uri": source.URI, "documents": source.Documents, "created_at": source.CreatedAt,
			"latest_import": source.LatestImport})
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
	uploads := make([]UploadedFile, 0, len(fileHeaders))
	for index, header := range fileHeaders {
		if header.Size > maxImportFileSize {
			httpapi.WriteError(w, 413, "file_too_large", "a manifest file exceeds 8 MiB")
			return
		}
		file, err := header.Open()
		if err != nil {
			httpapi.WriteError(w, 400, "invalid_package", "an uploaded file could not be read")
			return
		}
		temporary, createErr := os.CreateTemp("", "ecr-import-*")
		if createErr != nil {
			_ = file.Close()
			httpapi.WriteError(w, 400, "invalid_package", "an uploaded file could not be read")
			return
		}
		if chmodErr := temporary.Chmod(0o600); chmodErr != nil {
			_ = file.Close()
			_ = temporary.Close()
			_ = os.Remove(temporary.Name())
			httpapi.WriteError(w, 400, "invalid_package", "an uploaded file could not be read")
			return
		}
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
	defer func() {
		for _, upload := range uploads {
			_ = os.Remove(upload.TempPath)
		}
	}()
	parsed, err := parseManifest(manifest, uploads)
	if err != nil {
		httpapi.WriteError(w, 400, "invalid_package", "manifest package failed validation")
		return
	}
	queued, err := g.persistParsed(r.Context(), ParsedPackage{Manifest: manifest, Files: uploads, Documents: parsed}, 0, shopID)
	if err != nil {
		if errors.Is(err, errImportUpload) {
			httpapi.WriteError(w, 400, "invalid_package", "an uploaded file could not be read")
			return
		}
		if status.Code(err) == codes.InvalidArgument {
			httpapi.WriteError(w, 400, "invalid_package", "manifest package failed validation")
			return
		}
		if status.Code(err) == codes.AlreadyExists {
			httpapi.WriteError(w, 409, "version_conflict", "idempotency key was reused with a different package")
			return
		}
		writeQueueFailure(w, err)
		return
	}
	// The authorized Submit transaction creates the source and advances the
	// resource permission revision itself. Return its receipt instead of calling
	// that successful write a 403. Subsequent operations need a fresh session.
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"job_id": strconv.FormatInt(queued.JobID, 10),
		"manifest_sha256": queued.ManifestSHA256, "package_sha256": queued.PackageSHA256,
		"item_count": queued.ItemCount, "status": queued.Status, "is_mock": httpapi.IsMockProfile(g.Profile)})
}

func (g *Handler) persistParsed(ctx context.Context, parsed ParsedPackage, batchID int64, shopID string) (queuedPackage, error) {
	_ = batchID // stage 1 has no batch row; stage 2 binds the import batch here.
	files := make([]*ragv1.PackageFile, 0, len(parsed.Files))
	for _, upload := range parsed.Files {
		stream, err := os.Open(upload.TempPath)
		if err != nil {
			return queuedPackage{}, err
		}
		content, readErr := io.ReadAll(io.LimitReader(stream, maxImportFileSize+1))
		_ = stream.Close()
		if readErr != nil || len(content) > maxImportFileSize {
			return queuedPackage{}, errImportUpload
		}
		files = append(files, &ragv1.PackageFile{Path: upload.DeclaredName, Content: content})
	}
	return g.queuePackage(ctx, parsed.Manifest, files, g.Session.TenantID, g.Session.UserID, shopID)
}

func (g *Handler) adminJobs(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer, adminRoleObserver)
	if !ok {
		return
	}
	jobs, err := adminStore.Jobs(r.Context(), g.DB, g.Session.TenantID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion jobs")
		return
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, map[string]any{"job_id": strconv.FormatInt(job.ID, 10), "stage": job.Stage,
			"status": job.Status, "shop_id": job.ShopID, "manifest_sha256": job.ManifestHash,
			"dataset_sha256": job.DatasetHash, "fencing_epoch": job.FencingEpoch,
			"cancel_requested": job.CancelRequested, "item_count": job.Items, "pending": job.Pending,
			"running": job.Running, "awaiting_review": job.Awaiting, "done": job.Done, "failed": job.Failed,
			"created_at": job.CreatedAt, "updated_at": job.UpdatedAt})
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
	detail, err := adminStore.JobDetail(r.Context(), g.DB, g.Session.TenantID, jobID, scope.shops)
	if errors.Is(err, pgx.ErrNoRows) {
		httpapi.WriteError(w, 404, "not_found", "ingestion job not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion job")
		return
	}
	jobStatus, jobStage, shopID, manifestHash := detail.Status, detail.Stage, detail.ShopID, detail.ManifestHash
	datasetHash, errorCode := detail.DatasetHash, detail.ErrorCode
	fencingEpoch, cancelRequested, leaseExpiresAt := detail.FencingEpoch, detail.CancelRequested, detail.LeaseExpiresAt
	jobItems, err := adminStore.JobItems(r.Context(), g.DB, g.Session.TenantID, jobID, scope.shops)
	if err != nil {
		httpapi.WriteError(w, 503, "database_unavailable", "could not load ingestion items")
		return
	}
	items := make([]map[string]any, 0, len(jobItems))
	for _, item := range jobItems {
		items = append(items, map[string]any{"item_id": strconv.FormatInt(item.ID, 10), "document_id": item.DataID,
			"payload_hash": item.PayloadHash, "status": item.Status, "stage": item.Stage,
			"retry_count": item.RetryCount, "error_code": item.ErrorCode, "cancel_requested": item.CancelRequested,
			"fencing_epoch": item.FencingEpoch, "version_id": item.DocumentVersionID,
			"version_status": item.VersionStatus, "review_status": item.ReviewStatus,
			"version_hash": item.SourceHash})
	}
	if len(items) == 0 {
		var exists bool
		if exists, err = adminStore.JobExists(r.Context(), g.DB, g.Session.TenantID, jobID, scope.shops); err != nil {
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
