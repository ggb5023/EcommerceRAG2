package gateway

import (
	"context"
	"io"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc"
)

type generateTestStream struct {
	ragv1.RagService_GenerateClient
	events []*ragv1.GenerateResponse
	index  int
}

func (s *generateTestStream) Recv() (*ragv1.GenerateResponse, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

type generateTestClient struct {
	ragv1.RagServiceClient
	events []*ragv1.GenerateResponse
}

func (c *generateTestClient) Generate(context.Context, *ragv1.GenerateRequest, ...grpc.CallOption) (ragv1.RagService_GenerateClient, error) {
	return &generateTestStream{events: c.events}, nil
}

func testGenerationHooks() generationHooks {
	return generationHooks{
		onDelta:    func(string) *stageFailure { return nil },
		onCitation: func(*ragv1.Evidence) *stageFailure { return nil },
	}
}

func generatedDecision(behavior ragv1.ReplyBehavior, reason string) *ragv1.GenerateResponse {
	return &ragv1.GenerateResponse{Sequence: 1, Event: &ragv1.GenerateResponse_Decision{
		Decision: &ragv1.ReplyDecision{Behavior: behavior, ReasonCode: reason},
	}}
}

func generatedDone(sequence int32) *ragv1.GenerateResponse {
	return &ragv1.GenerateResponse{Sequence: sequence, Event: &ragv1.GenerateResponse_Done{Done: true}}
}

func TestGenerateAnswerHANDOFFWithoutEvidenceIsFailed(t *testing.T) {
	client := &generateTestClient{events: []*ragv1.GenerateResponse{
		generatedDecision(ragv1.ReplyBehavior_REPLY_BEHAVIOR_HANDOFF, "knowledge_missing"),
		generatedDone(2),
	}}
	request := &ragv1.GenerateRequest{Context: &ragv1.RequestContext{RequestId: "req-handoff"}}
	result, failure, aborted := generateAnswer(context.Background(), client, request, true,
		func() bool { return true }, testGenerationHooks())
	if aborted || failure != nil {
		t.Fatalf("generateAnswer aborted=%v failure=%+v", aborted, failure)
	}
	if result == nil || result.decision == nil {
		t.Fatalf("generateAnswer did not retain Decision: %+v", result)
	}
	state, code := generationTerminal(result.decision)
	if state != "FAILED" || code != "knowledge_missing" {
		t.Fatalf("terminal = %s/%s, want FAILED/knowledge_missing", state, code)
	}
	if terminalCanCopy(state) {
		t.Fatalf("FAILED terminal must not be copyable")
	}
}

func TestGenerateAnswerANSWERWithCitationRemainsDone(t *testing.T) {
	client := &generateTestClient{events: []*ragv1.GenerateResponse{
		generatedDecision(ragv1.ReplyBehavior_REPLY_BEHAVIOR_ANSWER, "grounded"),
		{Sequence: 2, Event: &ragv1.GenerateResponse_Citation{Citation: &ragv1.Evidence{Id: "evidence-1"}}},
		generatedDone(3),
	}}
	request := &ragv1.GenerateRequest{Context: &ragv1.RequestContext{RequestId: "req-answer"}}
	result, failure, aborted := generateAnswer(context.Background(), client, request, true,
		func() bool { return true }, testGenerationHooks())
	if aborted || failure != nil || result == nil || result.decision == nil {
		t.Fatalf("generateAnswer result=%+v failure=%+v aborted=%v", result, failure, aborted)
	}
	state, code := generationTerminal(result.decision)
	if state != "DONE" || code != "" {
		t.Fatalf("terminal = %s/%s, want DONE", state, code)
	}
	if !terminalCanCopy(state) {
		t.Fatalf("DONE terminal must remain copyable")
	}
}

// If a future implementation drops the Decision branch, this handoff case
// becomes DONE and fails; that is the regression this test guards.
func TestGenerateAnswerMustConsumeDecision(t *testing.T) {
	client := &generateTestClient{events: []*ragv1.GenerateResponse{
		generatedDecision(ragv1.ReplyBehavior_REPLY_BEHAVIOR_HANDOFF, "knowledge_missing"),
		generatedDone(2),
	}}
	result, _, _ := generateAnswer(context.Background(), client,
		&ragv1.GenerateRequest{Context: &ragv1.RequestContext{RequestId: "req-consume"}}, true,
		func() bool { return true }, testGenerationHooks())
	if result == nil || result.decision == nil {
		t.Fatal("Decision was silently dropped")
	}
}

func TestValidateCitationAcceptsAcceptedEvidence(t *testing.T) {
	original := evidence("a")
	copyOfOriginal := evidence("a")
	if !validateCitation(copyOfOriginal, []*ragv1.Evidence{original, evidence("b")}) {
		t.Fatal("an exact copy of an accepted candidate must validate")
	}
}

func TestValidateCitationRejectsUnknownOrAlteredEvidence(t *testing.T) {
	original := evidence("a")
	unknown := evidence("z")
	if validateCitation(unknown, []*ragv1.Evidence{original}) {
		t.Fatal("evidence outside the accepted set must be rejected")
	}
	altered := evidence("a")
	altered.Rank = 9
	if validateCitation(altered, []*ragv1.Evidence{original}) {
		t.Fatal("any field difference must be rejected")
	}
	if validateCitation(evidence("a"), nil) {
		t.Fatal("an empty accepted set must reject everything")
	}
	if validateCitation(nil, []*ragv1.Evidence{original}) {
		t.Fatal("a nil citation must be rejected")
	}
}

// evidence is the fixture helper shared by the gateway tests.
func evidence(id string) *ragv1.Evidence {
	return &ragv1.Evidence{Id: id, DocumentId: "doc-" + id, VersionId: "v-" + id,
		ShopId: "shop-a", DisclosureClass: "external_allowed", CustomerEligible: true, Rank: 1}
}
