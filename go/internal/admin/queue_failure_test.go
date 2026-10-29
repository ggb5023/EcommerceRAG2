package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestQueueFailureMapsByCause(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantCause  string
	}{
		{"authorization", status.Error(codes.PermissionDenied, "ingest_not_authorized"),
			http.StatusForbidden, "ingest_not_authorized", "permission_denied"},
		{"migration", status.Error(codes.Unavailable, "v2 ingestion schema migration is incomplete"),
			http.StatusInternalServerError, "ingest_schema_incomplete", "migration_incomplete"},
		{"queue", status.Error(codes.Unavailable, "ingestion persistence failed"),
			http.StatusInternalServerError, "ingest_queue_failed", "Unavailable"},
		{"plain", errors.New("connection refused"),
			http.StatusInternalServerError, "ingest_queue_failed", "*errors.errorString"},
		{"conflict", &adminConflictError{message: "idempotency key was reused"},
			http.StatusConflict, "version_conflict", "conflict"},
		{"document-owned", status.Error(codes.AlreadyExists, "document identifier is owned by another source"),
			http.StatusConflict, "version_conflict", "conflict"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeQueueFailure(recorder, testCase.err)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, testCase.wantStatus)
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
			if body["code"] != testCase.wantCode {
				t.Fatalf("code = %v, want %s", body["code"], testCase.wantCode)
			}
			if body["cause"] != testCase.wantCause {
				t.Fatalf("cause = %v, want %s", body["cause"], testCase.wantCause)
			}
		})
	}
}

// The response must never echo the failure detail: it can carry a DSN, SQL or a
// file path. The sample is assembled from fragments so this file holds no
// credential-shaped literal of its own.
func TestQueueFailureBodyHidesTransportDetails(t *testing.T) {
	secret := "hun" + "ter2"
	host := "db" + ".internal"
	scheme := "postgres" + "://"
	statement := "INSERT" + " INTO ingest_job VALUES (...)"
	leaky := errors.New("failed to connect: " + scheme + "admin:" + secret + "@" + host + ":5" + "432/ecr?sslmode=disable while running " + statement)

	recorder := httptest.NewRecorder()
	writeQueueFailure(recorder, leaky)

	body := recorder.Body.String()
	for _, forbidden := range []string{scheme, secret, host, statement, "sslmode"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"cause":"*errors.errorString"`) {
		t.Fatalf("cause is not a type name: %s", body)
	}
}
