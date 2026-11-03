package admin

import (
	"context"
	"encoding/json"
	"os"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const precheckTimeout = 15 * time.Second

type importRowError struct {
	Row             int32  `json:"row"`
	Column          string `json:"column"`
	Code            string `json:"code"`
	Severity        string `json:"severity"`
	Value           string `json:"value"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggested_action"`
}

// validatePackage invokes the read-only Python table precheck. Go still owns
// authorization and persistence; Python only reads the uploaded bytes.
func (g *Handler) validatePackage(ctx context.Context, manifest string, files []UploadedFile, shopID string) (*ragv1.ValidatePackageResponse, error) {
	if g.Ingest == nil {
		return nil, status.Error(codes.Unavailable, "package validation is unavailable")
	}
	packageFiles := make([]*ragv1.PackageFile, 0, len(files))
	for _, file := range files {
		content, err := os.ReadFile(file.TempPath)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "an uploaded file could not be read")
		}
		if len(content) > maxImportFileSize {
			return nil, status.Error(codes.InvalidArgument, "an uploaded file exceeds the size limit")
		}
		packageFiles = append(packageFiles, &ragv1.PackageFile{Path: file.DeclaredName, Content: content})
	}
	rpcCtx, cancel := context.WithTimeout(ctx, precheckTimeout)
	defer cancel()
	return g.Ingest.ValidatePackage(rpcCtx, &ragv1.ValidatePackageRequest{
		ManifestYaml: manifest,
		Files:        packageFiles,
		TenantId:     g.Session.Tenant,
		ShopId:       shopID,
	})
}

func importRowErrors(items []*ragv1.ValidatedPackageRowError) []importRowError {
	result := make([]importRowError, 0, len(items))
	for _, item := range items {
		result = append(result, importRowError{
			Row: item.GetRow(), Column: item.GetColumn(), Code: item.GetCode(),
			Severity: item.GetSeverity(), Value: item.GetValue(),
			Message: item.GetMessage(), SuggestedAction: item.GetSuggestedAction(),
		})
	}
	return result
}

func applyPackageValidation(detail *importStagedDetail, document *ragv1.ValidatedPackageDocument) {
	detail.TableSchema = document.GetTableSchema()
	detail.RowCount = document.GetRowCount()
	detail.ErrorCount = document.GetErrorCount()
	detail.RowErrors = importRowErrors(document.GetRowErrors())
	detail.Truncated = document.GetTruncated()
}

func rowErrorCounts(items []*ragv1.ValidatedPackageRowError) (blocks, warnings int) {
	for _, item := range items {
		switch item.GetSeverity() {
		case "block":
			blocks++
		case "warn":
			warnings++
		}
	}
	return blocks, warnings
}

func validationErrorResponse(err error) (int, string, string) {
	if status.Code(err) == codes.InvalidArgument {
		return 400, "invalid_package", "manifest package failed validation"
	}
	return 503, "precheck_unavailable", "import precheck is unavailable"
}

// blockingPrecheck reports whether either the document-level R1-R4 result or
// the typed row errors contain a block. Confirmation must fail closed.
func blockingPrecheck(precheckJSON, detailJSON []byte) bool {
	var issues []ImportIssue
	if json.Unmarshal(precheckJSON, &issues) == nil {
		for _, issue := range issues {
			if issue.Severity == "block" {
				return true
			}
		}
	}
	var detail importStagedDetail
	if json.Unmarshal(detailJSON, &detail) == nil {
		for _, issue := range detail.RowErrors {
			if issue.Severity == "block" {
				return true
			}
		}
	}
	return false
}
