package gateway

import (
	"context"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
	"github.com/ggb5023/EcommerceRAG/go/internal/observability"
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
	answer   string
	decision *ragv1.ReplyDecision
}

// generateAnswer consumes the generation stream and returns the answer text.
// It performs no persistence itself: deltas and citations go through hooks, and
// the caller turns the returned failure into finish(...).
//
// aborted is true when the authorization callback already finished the execution.
func generateAnswer(ctx context.Context, client ragv1.RagServiceClient, request *ragv1.GenerateRequest,
	mock bool, authorized func() bool, hooks generationHooks) (result *generationResult, failure *stageFailure, aborted bool) {
	ctx = observability.PrepareRPC(ctx, "gateway", "generate", request.GetContext().GetRequestId())
	gen, err := client.Generate(ctx, request, grpc.WaitForReady(true))
	if err != nil {
		return nil, grpcStageFailure(ctx, err), false
	}
	var answer strings.Builder
	var decision *ragv1.ReplyDecision
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
		case *ragv1.GenerateResponse_Decision:
			decision = value.Decision
			if decision != nil {
				observability.LogJSON("info", "gateway", "generate_decision", map[string]any{
					"request_id":  request.GetContext().GetRequestId(),
					"behavior":    decision.Behavior.String(),
					"reason_code": decision.ReasonCode,
				})
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
			return &generationResult{answer: answer.String(), decision: decision}, nil, false
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

// validateCitation is the thin wrapper over the pure domain rule.
func validateCitation(citation *ragv1.Evidence, accepted []*ragv1.Evidence) bool {
	return domain.ValidateCitation(citation, accepted)
}

// persistCitation stores one accepted citation for the execution.
func persistCitation(ctx context.Context, db *pgxpool.Pool, tenantID, executionID int64, citation *ragv1.Evidence) error {
	return insertCitation(ctx, db, tenantID, executionID, citation.Id, citation.Rank)
}

// runGenerationStage streams the answer and applies the snapshot qualifier. Hooks
// keep the delta and citation side effects out of the orchestrator.
func runGenerationStage(ctx context.Context, client ragv1.RagServiceClient, db *pgxpool.Pool, tenantID, executionID int64,
	request *ragv1.GenerateRequest, mock, snapshotFallback bool, accepted []*ragv1.Evidence,
	authorized func() bool, check func(*ragv1.Evidence) (bool, error),
	writeEvent func(string, any) error, onCitations func([]string)) (answer string, citations []string, decision *ragv1.ReplyDecision, failure *stageFailure, aborted bool) {
	cited := make([]string, 0, 8)
	defer func() { onCitations(cited) }()
	hooks := generationHooks{
		onDelta: func(text string) *stageFailure {
			if err := writeEvent("delta", map[string]any{"type": "delta", "text": text}); err != nil {
				return &stageFailure{state: "FAILED", code: "persistence_failed"}
			}
			return nil
		},
		onCitation: func(citation *ragv1.Evidence) *stageFailure {
			allowed, err := check(citation)
			if err != nil {
				return &stageFailure{state: "FAILED", code: authFailureCode(err)}
			}
			if !allowed || !validateCitation(citation, accepted) {
				return &stageFailure{state: "FAILED", code: "invalid_citation"}
			}
			if citation.DisclosureClass == "external_allowed" && citation.CustomerEligible {
				if err := persistCitation(ctx, db, tenantID, executionID, citation); err != nil {
					return &stageFailure{state: "FAILED", code: "persistence_failed"}
				}
			}
			// Only citations that passed validation and persistence are recorded.
			cited = append(cited, citation.Id)
			return nil
		},
	}
	generated, generateFailure, generateAborted := generateAnswer(ctx, client, request, mock, authorized, hooks)
	if generateAborted || generateFailure != nil {
		return "", cited, nil, generateFailure, generateAborted
	}
	return finalAnswer(generated, snapshotFallback), cited, generated.decision, nil, false
}

// generationTerminal maps the Python reply decision to the Go-owned terminal.
// CLARIFY intentionally stays DONE for this batch: mapping it to ASKING needs
// a separate decision. A missing decision preserves the legacy DONE behavior;
// the HANDOFF/empty-evidence path is carried by an explicit Decision.
func generationTerminal(decision *ragv1.ReplyDecision) (string, string) {
	if decision == nil {
		return "DONE", ""
	}
	if decision.Behavior == ragv1.ReplyBehavior_REPLY_BEHAVIOR_HANDOFF {
		code := strings.TrimSpace(decision.ReasonCode)
		if code == "" {
			code = "knowledge_missing"
		}
		return "FAILED", code
	}
	return "DONE", ""
}

// terminalCanCopy is the gateway-facing name for the single 0005 rule.
func terminalCanCopy(state, kind string, citationCount int, mock bool) (bool, string) {
	return domain.CanCopyReply(state, kind, citationCount, 0, mock)
}

// replyBehavior keeps the persisted contract short and stable.
func replyBehavior(decision *ragv1.ReplyDecision) string {
	if decision == nil {
		return "ANSWER" // legacy rows without a Decision keep the ANSWER path
	}
	switch decision.Behavior {
	case ragv1.ReplyBehavior_REPLY_BEHAVIOR_ANSWER:
		return "ANSWER"
	case ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY:
		return "CLARIFY"
	case ragv1.ReplyBehavior_REPLY_BEHAVIOR_HANDOFF:
		return "HANDOFF"
	default:
		return decision.Behavior.String()
	}
}

// finishGeneration keeps the terminal mapping out of runTurn's line budget.
func finishGeneration(finish func(string, string, string), setBehavior func(string),
	decision *ragv1.ReplyDecision, answer string) {
	state, code := generationTerminal(decision)
	setBehavior(replyBehavior(decision))
	finish(state, code, answer)
}

// authFailureCode maps an authorization error to its stable failure code.
func authFailureCode(err error) string { return auth.FailureCode(err) }
