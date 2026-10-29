package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// queueFailureResponse maps an ingestion queue error to a status, a stable code
// and a short cause identifier.
//
// The cause is deliberately coarse: it is an error type name or a gRPC status
// code, never a message. SQL, DSNs, file paths and credentials must not reach the
// response body.
func queueFailureResponse(err error) (statusCode int, code, cause string) {
	var conflict *adminConflictError
	switch {
	case errors.As(err, &conflict):
		return http.StatusConflict, "version_conflict", "conflict"
	case status.Code(err) == codes.AlreadyExists:
		// The confirm path can hit IngestConflictError too (document id owned by
		// another source); it must not look like a transient queue failure.
		return http.StatusConflict, "version_conflict", "conflict"
	case status.Code(err) == codes.PermissionDenied:
		return http.StatusForbidden, "ingest_not_authorized", "permission_denied"
	case isSchemaIncomplete(err):
		return http.StatusInternalServerError, "ingest_schema_incomplete", "migration_incomplete"
	default:
		return http.StatusInternalServerError, "ingest_queue_failed", queueCause(err)
	}
}

// isSchemaIncomplete detects the migration guard without echoing the message.
func isSchemaIncomplete(err error) bool {
	return strings.Contains(strings.ToLower(status.Convert(err).Message()), "migration")
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
	statusCode, code, cause := queueFailureResponse(err)
	httpapi.WriteErrorCause(w, statusCode, code, "package could not be queued", cause)
}
