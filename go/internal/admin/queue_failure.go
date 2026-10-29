package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// conflictMessage is actionable: the merchant must pick a new identity or clean
// up the owning source first.
const conflictMessage = "该文档标识已被另一个来源占用；请使用新的标识，或撤回后先清理来源"

// queueFailureResponse maps an ingestion queue error to a status, a stable code,
// a merchant-facing message and a short cause identifier.
//
// The mapping reads the gRPC code only. Error messages are never inspected: they
// can carry SQL, DSNs, file paths or credentials, so the cause stays coarse (a
// status code name or a Go type name).
func queueFailureResponse(err error) (statusCode int, code, message, cause string) {
	var conflict *adminConflictError
	switch {
	case errors.As(err, &conflict):
		return http.StatusConflict, "version_conflict", conflictMessage, "conflict"
	case status.Code(err) == codes.AlreadyExists:
		// The confirm path reaches IngestConflictError too (a document id owned by
		// another source); it must not look like a transient queue failure.
		return http.StatusConflict, "version_conflict", conflictMessage, "conflict"
	case status.Code(err) == codes.PermissionDenied:
		return http.StatusForbidden, "ingest_not_authorized", "package could not be queued", "permission_denied"
	case status.Code(err) == codes.FailedPrecondition:
		return http.StatusInternalServerError, "ingest_schema_incomplete", "package could not be queued", "migration_incomplete"
	case status.Code(err) == codes.Unavailable:
		return http.StatusServiceUnavailable, "ingest_queue_failed", "package could not be queued", "Unavailable"
	default:
		return http.StatusInternalServerError, "ingest_queue_failed", "package could not be queued", queueCause(err)
	}
}

// queueCause returns a short identifier for the error class.
func queueCause(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case status.Code(err) != codes.Unknown:
		return status.Code(err).String()
	default:
		return fmt.Sprintf("%T", err)
	}
}

// writeQueueFailure renders the mapping above.
func writeQueueFailure(w http.ResponseWriter, err error) {
	statusCode, code, message, cause := queueFailureResponse(err)
	httpapi.WriteErrorCause(w, statusCode, code, message, cause)
}
