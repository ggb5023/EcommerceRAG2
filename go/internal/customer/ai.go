package customer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/observability"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type proposal struct {
	Answer   string
	Decision *ragv1.ReplyDecision
	Evidence []*ragv1.Evidence
	Mock     bool
}

// ValidateProposal checks structure and provenance. Semantic correctness is
// separately measured by the frozen release evaluation, not inferred from JSON.
func ValidateProposal(p proposal) error {
	if p.Decision == nil || strings.TrimSpace(p.Answer) == "" || len(p.Answer) > 32768 || p.Mock {
		return errors.New("delivery_rejected")
	}
	behavior, reason := p.Decision.Behavior, p.Decision.ReasonCode
	switch behavior {
	case ragv1.ReplyBehavior_REPLY_BEHAVIOR_ANSWER:
		if reason != "grounded" || len(p.Evidence) == 0 {
			return errors.New("missing_support")
		}
	case ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY:
		if reason != "missing_context" || len(p.Answer) > 1024 {
			return errors.New("invalid_clarification")
		}
	case ragv1.ReplyBehavior_REPLY_BEHAVIOR_HANDOFF:
		if reason != "knowledge_missing" && reason != "conflicting_evidence" && reason != "unsupported_request" {
			return errors.New("invalid_handoff")
		}
	default:
		return errors.New("decision_missing")
	}
	ids := map[string]bool{}
	ranks := map[int32]bool{}
	for _, e := range p.Evidence {
		if e == nil || e.Id == "" || e.Rank <= 0 || ids[e.Id] || ranks[e.Rank] || !e.CustomerEligible || e.DisclosureClass != "external_allowed" {
			return errors.New("invalid_citation")
		}
		ids[e.Id] = true
		ranks[e.Rank] = true
	}
	decisionIDs := map[string]bool{}
	for _, id := range p.Decision.EvidenceIds {
		if !ids[id] || decisionIDs[id] {
			return errors.New("decision_citation_mismatch")
		}
		decisionIDs[id] = true
	}
	if len(decisionIDs) != len(ids) {
		return errors.New("decision_citation_mismatch")
	}
	return nil
}
func (s *Service) run(execution, conversationID, sessionID, query string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s.mu.Lock()
	s.cancels[conversationID] = activeRun{execution: execution, cancel: cancel}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if run, ok := s.cancels[conversationID]; ok && run.execution == execution {
			delete(s.cancels, conversationID)
		}
		s.mu.Unlock()
	}()
	p := proposal{}
	reason := "provider_failed"
	stage := "control"
	var scope *ragv1.RequestContext
	defer func() {
		finishCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if reason == "provider_failed" {
			reason = "provider_failed_" + stage
		}
		_ = s.finish(finishCtx, execution, conversationID, scope, p, reason)
	}()
	select {
	case s.aiSlots <- struct{}{}:
		defer func() { <-s.aiSlots }()
	default:
		return
	}
	var err error
	if err = markCustomerExecutionExecuting(ctx, s.DB, execution); err != nil {
		return
	}
	if strings.Contains(query, "转人工") || strings.Contains(query, "人工客服") || strings.Contains(query, "真人客服") {
		reason = "customer_requested"
		return
	}
	scope, err = auth.IssueCustomerScope(ctx, s.DB, s.SigningKey, sessionID, conversationID, execution)
	if err != nil {
		if err.Error() == "knowledge_missing" {
			reason = "knowledge_missing"
		}
		return
	}
	validate := func() error { return (&auth.Authority{DB: s.DB, SigningKey: s.SigningKey}).ValidateScope(ctx, scope) }
	history := s.history(ctx, conversationID)
	ctx = observability.PrepareRPC(ctx, "customer", "understand", scope.GetRequestId())
	understood, err := s.Rag.Understand(ctx, &ragv1.UnderstandRequest{Context: scope, Query: query, HistorySummary: history})
	if err != nil {
		stage = "control_" + status.Code(err).String()
		return
	}
	if understood == nil || validate() != nil {
		return
	}
	if len(understood.RequiredFactTools) > 0 || understood.InformationSource == "realtime_business" {
		reason = "unsupported_request"
		return
	}
	if understood.Clarification != "" {
		p = proposal{Answer: understood.Clarification, Decision: &ragv1.ReplyDecision{Behavior: ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY, ReasonCode: "missing_context"}, Mock: s.Profile != "v2_langchain_bailian"}
		reason = ""
		return
	}
	rewritten := understood.RewrittenQuery
	if rewritten == "" {
		rewritten = query
	}
	stage = "search"
	ctx = observability.PrepareRPC(ctx, "customer", "search", scope.GetRequestId())
	search, err := s.Rag.Search(ctx, &ragv1.SearchRequest{Context: scope, Query: rewritten, ResponsePurpose: "customer_chat"})
	if err != nil {
		stage = "search_" + status.Code(err).String()
		return
	}
	evidence := []*ragv1.Evidence{}
	ids := map[string]bool{}
	ranks := map[int32]bool{}
	complete := false
	for {
		part, err := search.Recv()
		if err != nil {
			stage = "search_" + status.Code(err).String()
			return
		}
		if validate() != nil || part.IsMock || part.ErrorCode != "" {
			return
		}
		for _, item := range part.Evidence {
			stage = "evidence_validation"
			allowed, err := auth.CustomerEvidenceAllowed(ctx, s.DB, scope.TenantDbId, scope.ShopId, item)
			if err != nil || !allowed || ids[item.Id] || ranks[item.Rank] || item.Rank <= 0 || len(evidence) >= 8 {
				return
			}
			evidence = append(evidence, item)
			ids[item.Id] = true
			ranks[item.Rank] = true
		}
		if part.Complete {
			complete = true
			break
		}
	}
	if !complete || len(evidence) == 0 {
		reason = "knowledge_missing"
		return
	}
	stage = "generation"
	ctx = observability.PrepareRPC(ctx, "customer", "generate", scope.GetRequestId())
	generated, err := s.Rag.Generate(ctx, &ragv1.GenerateRequest{Context: scope, Query: query, Evidence: evidence, ResponsePurpose: "customer_chat", HistorySummary: history})
	if err != nil {
		return
	}
	var text strings.Builder
	var sequence uint64
	profileSeen, done := false, false
	for {
		part, err := generated.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return
		}
		if validate() != nil || done || part.Sequence != sequence+1 {
			return
		}
		sequence = part.Sequence
		switch event := part.Event.(type) {
		case *ragv1.GenerateResponse_Delta:
			text.WriteString(event.Delta)
			if text.Len() > 32768 {
				return
			}
		case *ragv1.GenerateResponse_Decision:
			if p.Decision != nil {
				return
			}
			p.Decision = event.Decision
		case *ragv1.GenerateResponse_Citation:
			canonical := false
			for _, item := range evidence {
				canonical = canonical || proto.Equal(item, event.Citation)
			}
			if !canonical {
				return
			}
			p.Evidence = append(p.Evidence, event.Citation)
		case *ragv1.GenerateResponse_IsMock:
			if profileSeen {
				return
			}
			profileSeen = true
			p.Mock = event.IsMock
		case *ragv1.GenerateResponse_ErrorCode:
			return
		case *ragv1.GenerateResponse_Done:
			if !event.Done {
				return
			}
			done = true
		}
	}
	p.Answer = text.String()
	if !done || !profileSeen || ValidateProposal(p) != nil {
		return
	}
	reason = ""
}
func (s *Service) history(ctx context.Context, id string) string {
	texts, err := loadCustomerHistoryTexts(ctx, s.DB, id)
	if err != nil {
		return ""
	}
	skip := true
	size := 0
	selected := []string{}
	for _, text := range texts {
		if skip {
			skip = false
			continue
		}
		if size+len(text) > 7500 {
			break
		}
		selected = append(selected, text)
		size += len(text)
	}
	items := []map[string]string{}
	for i := len(selected) - 1; i >= 0; i-- {
		items = append(items, map[string]string{"role": "user", "content": selected[i]})
	}
	encoded, _ := json.Marshal(items)
	if len(encoded) > 8192 {
		return ""
	}
	return string(encoded)
}
func (s *Service) finish(ctx context.Context, execution, id string, scope *ragv1.RequestContext, p proposal, reason string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := lockConversation(ctx, tx, id)
	if err != nil {
		return err
	}
	epoch, state, err := lockCustomerExecution(ctx, tx, execution)
	if err != nil {
		return err
	}
	if c.State != "AI" || c.Epoch != epoch || c.Active == nil || *c.Active != execution || (state != "PENDING" && state != "EXECUTING") {
		return nil
	}
	reason, err = s.finishReason(ctx, tx, c, scope, p, reason)
	if err != nil {
		return err
	}
	if reason != "" {
		return s.finishFailure(ctx, tx, c, execution, reason)
	}
	return s.finishSuccess(ctx, tx, c, execution, p)
}

func (s *Service) finishReason(ctx context.Context, tx pgx.Tx, c conversation, scope *ragv1.RequestContext, p proposal, reason string) (string, error) {
	if reason != "" {
		return reason, nil
	}
	if ValidateProposal(p) != nil || scope == nil {
		return "provider_failed", nil
	}
	// Every ACL or publication change bumps and locks this revision. Holding
	// its share lock serializes publication with revocation at commit time.
	revision, err := lockShopPublicRevision(ctx, tx, c.Tenant, c.Shop)
	if err != nil || revision != scope.PermissionRevision || scope.ConversationEpoch != uint64(c.Epoch) || scope.ScopeExpiresAt <= time.Now().Unix() {
		reason = "permission_changed"
	}
	if reason == "" {
		active, err := lockCustomerSessionActive(ctx, tx, scope.CustomerSessionId)
		if err != nil || !active {
			reason = "permission_changed"
		}
	}
	for _, item := range p.Evidence {
		if reason != "" {
			break
		}
		allowed, err := auth.CustomerEvidenceAllowed(ctx, s.DB, c.Tenant, c.Shop, item)
		if err != nil || !allowed {
			reason = "permission_changed"
		}
	}
	return reason, nil
}

func (s *Service) finishFailure(ctx context.Context, tx pgx.Tx, c conversation, execution, reason string) error {
	if err := failCustomerExecution(ctx, tx, execution, reason); err != nil {
		return err
	}
	if err := queueTx(ctx, tx, c, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) finishSuccess(ctx context.Context, tx pgx.Tx, c conversation, execution string, p proposal) error {
	behavior := p.Decision.Behavior
	executionState := "DONE"
	if behavior == ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY && c.Clarifications >= 1 {
		behavior = ragv1.ReplyBehavior_REPLY_BEHAVIOR_HANDOFF
		p.Decision.ReasonCode = "clarification_exhausted"
	}
	if behavior == ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY {
		executionState = "ASKING"
	}
	raw, err := json.Marshal(p.Evidence)
	if err != nil {
		return err
	}
	if err = completeCustomerExecution(ctx, tx, execution, executionState, behavior.String(), p.Decision.ReasonCode, p.Answer, raw, p.Mock); err != nil {
		return err
	}
	if behavior == ragv1.ReplyBehavior_REPLY_BEHAVIOR_HANDOFF {
		if err = queueTx(ctx, tx, c, p.Decision.ReasonCode); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	c.Clarifications = 0
	if behavior == ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY {
		c.Clarifications = 1
	}
	if err = clearCustomerActiveExecution(ctx, tx, c.ID, c.Clarifications); err != nil {
		return err
	}
	if _, err = addMessage(ctx, tx, c, "assistant", p.Answer, execution, nil, p.Evidence); err != nil {
		return err
	}
	if err = emit(ctx, tx, c, "completed", map[string]any{"execution_id": execution, "behavior": behavior.String()}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
