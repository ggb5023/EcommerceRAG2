package gateway

import (
	"context"
	"strings"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// generationHooks receives the two side effects of the generation stream:
// persisting a delta frame and accepting a citation. Both return a stage failure
// to stop the stream with a terminal code.
type generationHooks struct {
	onDelta    func(text string) *stageFailure
	onCitation func(citation *ragv1.Evidence) *stageFailure
}

// generationResult is the text accumulated from delta frames.
type generationResult struct {
	answer string
}

// generateAnswer consumes the generation stream and returns the answer text.
// It performs no persistence itself: deltas and citations go through hooks, and
// the caller turns the returned failure into finish(...).
//
// aborted is true when the authorization callback already finished the execution.
func generateAnswer(ctx context.Context, client ragv1.RagServiceClient, request *ragv1.GenerateRequest,
	mock bool, authorized func() bool, hooks generationHooks) (result *generationResult, failure *stageFailure, aborted bool) {
	gen, err := client.Generate(ctx, request, grpc.WaitForReady(true))
	if err != nil {
		return nil, grpcStageFailure(ctx, err), false
	}
	var answer strings.Builder
	isMock := mock
	sawProfileMarker := false
	for {
		if !authorized() {
			return nil, nil, true
		}
		part, recvErr := gen.Recv()
		if recvErr != nil {
			if recvErr == context.Canceled || recvErr == context.DeadlineExceeded || status.Code(recvErr) == codes.Canceled || status.Code(recvErr) == codes.DeadlineExceeded {
				return nil, grpcStageFailure(ctx, recvErr), false
			}
			if recvErr == context.Canceled {
				return nil, &stageFailure{state: "CANCELLED", code: "cancelled"}, false
			}
			return nil, &stageFailure{state: "FAILED", code: "rag_generate_failed"}, false
		}
		if !authorized() {
			return nil, nil, true
		}
		switch value := part.Event.(type) {
		case *ragv1.GenerateResponse_Delta:
			answer.WriteString(value.Delta)
			if stageErr := hooks.onDelta(value.Delta); stageErr != nil {
				return nil, stageErr, false
			}
		case *ragv1.GenerateResponse_Citation:
			if stageErr := hooks.onCitation(value.Citation); stageErr != nil {
				return nil, stageErr, false
			}
		case *ragv1.GenerateResponse_IsMock:
			sawProfileMarker = true
			isMock = value.IsMock
			if isMock != mock {
				return nil, &stageFailure{state: "FAILED", code: "provider_profile_mismatch"}, false
			}
		case *ragv1.GenerateResponse_ErrorCode:
			return nil, &stageFailure{state: "FAILED", code: value.ErrorCode}, false
		case *ragv1.GenerateResponse_Done:
			if !value.Done {
				continue
			}
			if !mock && !sawProfileMarker {
				return nil, &stageFailure{state: "FAILED", code: "provider_profile_mismatch"}, false
			}
			if isMock != mock {
				return nil, &stageFailure{state: "FAILED", code: "provider_profile_mismatch"}, false
			}
			return &generationResult{answer: answer.String()}, nil, false
		}
	}
}

// finalAnswer applies the snapshot qualifier to a finished answer. Pure.
func finalAnswer(result *generationResult, snapshotFallback bool) string {
	text := result.answer
	if snapshotFallback {
		text = withSnapshotQualifier(text)
	}
	return text
}

// validateCitation reports whether the cited evidence is one of the accepted
// candidates. It is pure: it only compares protobuf messages.
func validateCitation(citation *ragv1.Evidence, accepted []*ragv1.Evidence) bool {
	for _, original := range accepted {
		if evidenceEqual(original, citation) {
			return true
		}
	}
	return false
}

// persistCitation stores one accepted citation for the execution.
func persistCitation(ctx context.Context, db *pgxpool.Pool, tenantID, executionID int64, citation *ragv1.Evidence) error {
	_, err := db.Exec(ctx, `INSERT INTO citation(tenant_id,execution_id,evidence_id,citation_index)
		VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenantID, executionID, citation.Id, int(citation.Rank))
	return err
}
