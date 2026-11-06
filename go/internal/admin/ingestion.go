package admin

import (
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"

	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"net/http"
	"strconv"
	"strings"
)

type queuedPackage struct {
	JobID                         int64
	ManifestSHA256, PackageSHA256 string
	ItemCount                     int
	Status                        string
}
type adminJobConfirmation struct {
	Confirm              bool   `json:"confirm"`
	TenantID             string `json:"tenant_id"`
	ShopID               string `json:"shop_id"`
	JobID                string `json:"job_id"`
	PermissionRevision   string `json:"permission_revision"`
	ExpectedFencingEpoch int64  `json:"expected_fencing_epoch"`
	Reason               string `json:"reason"`
}

func (g *Handler) queuePackage(ctx context.Context, manifest string, files []*ragv1.PackageFile,
	tenantID, userID int64, shopID string, batchID int64) (queuedPackage, error) {
	scope, err := g.Authority.IssueScopeForShop(ctx, httpapi.NewRequestID(), shopID)
	if err != nil {
		return queuedPackage{}, err
	}
	scope.ScopeAudience = "ingest"
	scope.ScopeSignature = hex.EncodeToString(auth.MAC(g.SigningKey, scope))
	if g.Authority.RegisterScopeIdentity != nil {
		if err = g.Authority.RegisterScopeIdentity(scope); err != nil {
			return queuedPackage{}, err
		}
	}
	key := ingestIdempotencyKey(shopID, batchID, manifest)
	// Batches created before the batch-scoped key may still have an unfinished
	// job under the legacy key. Reuse it rather than parsing the package twice.
	if batchID > 0 {
		if legacy, found, lookupErr := adminStore.UnfinishedJobByKey(ctx, g.DB, tenantID, shopID,
			legacyIngestIdempotencyKey(shopID, manifest)); lookupErr != nil {
			return queuedPackage{}, lookupErr
		} else if found {
			return queuedPackage{JobID: legacy.ID, ManifestSHA256: legacy.ManifestSHA256,
				PackageSHA256: legacy.PackageSHA256, ItemCount: int(legacy.ItemCount), Status: legacy.Status}, nil
		}
	}
	response, err := g.Ingest.Submit(ctx, &ragv1.SubmitRequest{
		Context: scope, IdempotencyKey: key, ManifestYaml: manifest, Files: files,
		TenantDbId: strconv.FormatInt(tenantID, 10), UserDbId: strconv.FormatInt(userID, 10), ShopId: shopID,
	})
	if err != nil {
		return queuedPackage{}, err
	}
	jobID, err := strconv.ParseInt(response.GetJobId(), 10, 64)
	if err != nil {
		return queuedPackage{}, errors.New("python ingestion returned an invalid job id")
	}
	return queuedPackage{
		JobID:          jobID,
		ManifestSHA256: response.GetManifestSha256(),
		PackageSHA256:  response.GetPackageSha256(),
		ItemCount:      int(response.GetItemCount()),
		Status:         jobStatusText(response.GetStatus()),
	}, nil
}

func jobStatusText(value ragv1.JobStatus) string {
	switch value {
	case ragv1.JobStatus_JOB_STATUS_PENDING:
		return "pending"
	case ragv1.JobStatus_JOB_STATUS_RUNNING:
		return "running"
	case ragv1.JobStatus_JOB_STATUS_AWAITING_REVIEW:
		return "awaiting_review"
	case ragv1.JobStatus_JOB_STATUS_DONE:
		return "done"
	case ragv1.JobStatus_JOB_STATUS_FAILED:
		return "failed"
	case ragv1.JobStatus_JOB_STATUS_CANCELLED:
		return "cancelled"
	default:
		return "unknown"
	}
}

func (g *Handler) adminCancelIngestJob(w http.ResponseWriter, r *http.Request) {
	g.adminJobCommand(w, r, ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_CANCEL)
}
func (g *Handler) adminRetryIngestJob(w http.ResponseWriter, r *http.Request) {
	g.adminJobCommand(w, r, ragv1.IngestCommandAction_INGEST_COMMAND_ACTION_RETRY)
}
func decodeAdminJobConfirmation(w http.ResponseWriter, r *http.Request, input *adminJobConfirmation) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	if input.JobID == "" || !input.Confirm || len(strings.TrimSpace(input.Reason)) < 10 || len(input.Reason) > 2000 {
		return errors.New("job confirmation fields are incomplete")
	}
	return nil
}

func validJobConfirmation(input adminJobConfirmation, session auth.Session, jobID int64) bool {
	return input.Confirm && input.JobID == strconv.FormatInt(jobID, 10) &&
		input.TenantID == session.Tenant && input.PermissionRevision == session.PermissionRevision &&
		len(strings.TrimSpace(input.Reason)) >= 10 && len(input.Reason) <= 2000
}

// ingestIdempotencyKey is the key the ingest service deduplicates on. The batch
// id keeps two same-content batches as two jobs; a retry of one batch still
// reuses its own job. The legacy direct-import path has no batch row, so it
// keeps the old shop+manifest key.
func ingestIdempotencyKey(shopID string, batchID int64, manifest string) string {
	if batchID <= 0 {
		return legacyIngestIdempotencyKey(shopID, manifest)
	}
	return "admin:" + shopID + ":" + strconv.FormatInt(batchID, 10) + ":" + manifestDigest(manifest)
}

// legacyIngestIdempotencyKey is the pre-batch key. It is only consulted to adopt
// an unfinished job; completed legacy jobs must not absorb a new batch.
func legacyIngestIdempotencyKey(shopID, manifest string) string {
	return "admin:" + shopID + ":" + manifestDigest(manifest)
}

func manifestDigest(manifest string) string {
	digest := sha256.Sum256([]byte(manifest))
	return hex.EncodeToString(digest[:])
}
