package customer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
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
	Facts    []*ragv1.Fact
	Mock     bool
}

// customerFactTools is the read-only set an after-sales answer may rely on.
// compare_products stays outside the customer channel.
var customerFactTools = map[string]string{
	"get_order_status": "order_id",
	"get_logistics":    "order_id",
	"get_stock":        "sku",
	"get_price":        "sku",
}

func validCustomerFact(fact *ragv1.Fact) bool {
	if fact == nil || strings.TrimSpace(fact.ValueJson) == "" || fact.ResourceId == "" {
		return false
	}
	field, ok := customerFactTools[fact.ToolName]
	if !ok || fact.ResourceId == "" {
		return false
	}
	var value map[string]any
	if json.Unmarshal([]byte(fact.ValueJson), &value) != nil || value[field] != fact.ResourceId {
		return false
	}
	_, err := time.Parse(time.RFC3339, fact.ObservedAt)
	return err == nil
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
		if reason != "grounded" || (len(p.Evidence) == 0 && len(p.Facts) == 0) {
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
	if len(p.Facts) > 4 {
		return errors.New("too_many_facts")
	}
	for _, fact := range p.Facts {
		if !validCustomerFact(fact) {
			return errors.New("invalid_fact")
		}
	}
	return nil
}

type customerRunState struct {
	p           proposal
	reason      string
	stage       string
	scope       *ragv1.RequestContext
	history     string
	rewritten   string
	evidence    []*ragv1.Evidence
	facts       []*ragv1.Fact
	orderTools  []string
	productTool []string
	sku         string
}

func (s *Service) validateCustomerScope(ctx context.Context, scope *ragv1.RequestContext) error {
	return (&auth.Authority{DB: s.DB, SigningKey: s.SigningKey}).ValidateScope(ctx, scope)
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
	state := &customerRunState{reason: "provider_failed", stage: "control"}
	defer func() {
		finishCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if state.reason == "provider_failed" {
			state.reason = "provider_failed_" + state.stage
		}
		_ = s.finish(finishCtx, execution, conversationID, state.scope, state.p, state.reason)
	}()
	select {
	case s.aiSlots <- struct{}{}:
		defer func() { <-s.aiSlots }()
	default:
		return
	}
	if err := markCustomerExecutionExecuting(ctx, s.DB, execution); err != nil {
		return
	}
	if strings.Contains(query, "转人工") || strings.Contains(query, "人工客服") || strings.Contains(query, "真人客服") {
		state.reason = "customer_requested"
		return
	}
	if !s.understandCustomer(ctx, state, query, sessionID, conversationID, execution) {
		return
	}
	if !s.lookupCustomerFacts(ctx, state) {
		return
	}
	if !s.searchCustomerEvidence(ctx, state, query, len(state.facts) > 0) {
		return
	}
	if !s.generateCustomerAnswer(ctx, state, query) {
		return
	}
	state.reason = ""
}

func (s *Service) understandCustomer(ctx context.Context, state *customerRunState, query, sessionID, conversationID, execution string) bool {
	scope, err := auth.IssueCustomerScope(ctx, s.DB, s.SigningKey, sessionID, conversationID, execution)
	if err != nil {
		if err.Error() == "knowledge_missing" {
			state.reason = "knowledge_missing"
		}
		return false
	}
	state.scope = scope
	state.history = s.history(ctx, conversationID)
	ctx = observability.PrepareRPC(ctx, "customer", "understand", scope.GetRequestId())
	understood, err := s.Rag.Understand(ctx, &ragv1.UnderstandRequest{Context: scope, Query: query, HistorySummary: state.history})
	if err != nil {
		state.stage = "control_" + status.Code(err).String()
		return false
	}
	if understood == nil || s.validateCustomerScope(ctx, scope) != nil {
		return false
	}
	if len(understood.RequiredFactTools) > 0 || understood.InformationSource == "realtime_business" {
		if scope.ServiceType != "after_sales" {
			state.reason = "unsupported_request"
			return false
		}
		orderTools, productTools, sku, reason := afterSalesFactPlan(understood, scope.CustomerOrderId, query)
		if reason == "sku_clarification" || reason == "order_clarification" {
			message := "请确认唯一商品 SKU 后再查询当前库存或价格。"
			if reason == "order_clarification" {
				message = "本次售后会话只绑定一个订单，请确认要查询的订单号。"
			}
			state.p = proposal{Answer: message, Decision: &ragv1.ReplyDecision{Behavior: ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY, ReasonCode: "missing_context"}, Mock: s.Profile != "v2_langchain_bailian"}
			state.reason = ""
			return false
		}
		if reason != "" {
			state.reason = reason
			return false
		}
		state.orderTools, state.productTool, state.sku = orderTools, productTools, sku
	}
	if understood.Clarification != "" {
		state.p = proposal{Answer: understood.Clarification, Decision: &ragv1.ReplyDecision{Behavior: ragv1.ReplyBehavior_REPLY_BEHAVIOR_CLARIFY, ReasonCode: "missing_context"}, Mock: s.Profile != "v2_langchain_bailian"}
		state.reason = ""
		return false
	}
	state.rewritten = understood.RewrittenQuery
	if state.rewritten == "" {
		state.rewritten = query
	}
	return true
}

// afterSalesFactPlan validates the model's read-only tool request. The order is
// always the one bound to the conversation; a different explicit order becomes
// a clarification, never a lookup.
func afterSalesFactPlan(understood *ragv1.UnderstandResponse, orderID, query string) (orderTools, productTools []string, sku, reason string) {
	for _, tool := range understood.RequiredFactTools {
		switch tool {
		case "get_order_status", "get_logistics":
			orderTools = append(orderTools, tool)
		case "get_stock", "get_price":
			productTools = append(productTools, tool)
		default:
			return nil, nil, "", "unsupported_request"
		}
	}
	for _, entity := range understood.Entities {
		if entity == nil || entity.Name != "order_id" {
			continue
		}
		value := strings.ToUpper(strings.TrimSpace(entity.Value))
		if value != "" && value != orderID {
			return nil, nil, "", "order_clarification"
		}
	}
	if len(orderTools) == 0 && len(productTools) == 0 {
		// realtime_business without a supported tool means a write/unknown
		// capability; human staff handle it.
		return nil, nil, "", "unsupported_request"
	}
	if len(productTools) > 0 {
		value, ok := routedCustomerSKU(understood, query, orderID)
		if !ok {
			return nil, nil, "", "sku_clarification"
		}
		sku = value
	}
	return orderTools, productTools, sku, ""
}

// routedCustomerSKU accepts only one explicit SKU-shaped token in the current
// message. History is deliberately not used: an after-sales lookup must be
// restated in the request that authorizes it.
func routedCustomerSKU(understood *ragv1.UnderstandResponse, query, orderID string) (string, bool) {
	id := ""
	for _, entity := range understood.Entities {
		if entity == nil || entity.Name != "sku" {
			continue
		}
		value := strings.TrimSpace(entity.Value)
		if id != "" || value == "" || !skuPattern.MatchString(value) {
			return "", false
		}
		id = value
	}
	if id == "" {
		return "", false
	}
	tokens := map[string]bool{}
	for _, token := range skuTokenPattern.FindAllString(query, -1) {
		if !strings.EqualFold(token, orderID) {
			tokens[token] = true
		}
	}
	if len(tokens) != 1 {
		return "", false
	}
	for token := range tokens {
		return id, token == id
	}
	return "", false
}

type factLookup struct {
	names    []string
	resource string
}

func (s *Service) lookupCustomerFacts(ctx context.Context, state *customerRunState) bool {
	if len(state.orderTools) == 0 && len(state.productTool) == 0 {
		return true
	}
	provider := s.Facts
	if provider == nil {
		provider = facts.UnavailableProvider{}
	}
	lookups := []factLookup{}
	if len(state.orderTools) > 0 {
		lookups = append(lookups, factLookup{state.orderTools, state.scope.CustomerOrderId})
	}
	if len(state.productTool) > 0 {
		lookups = append(lookups, factLookup{state.productTool, state.sku})
	}
	for _, item := range lookups {
		rows, err := facts.Lookup(ctx, provider, state.scope, item.names, item.resource)
		if err != nil {
			state.stage = "facts"
			state.reason = facts.FailureCode(err)
			return false
		}
		state.facts = append(state.facts, rows...)
	}
	return true
}

func (s *Service) searchCustomerEvidence(ctx context.Context, state *customerRunState, query string, allowEmpty bool) bool {
	state.stage = "search"
	ctx = observability.PrepareRPC(ctx, "customer", "search", state.scope.GetRequestId())
	search, err := s.Rag.Search(ctx, &ragv1.SearchRequest{Context: state.scope, Query: state.rewritten, ResponsePurpose: "customer_chat"})
	if err != nil {
		state.stage = "search_" + status.Code(err).String()
		return false
	}
	evidence := []*ragv1.Evidence{}
	ids := map[string]bool{}
	ranks := map[int32]bool{}
	complete := false
	for {
		part, err := search.Recv()
		if err != nil {
			state.stage = "search_" + status.Code(err).String()
			return false
		}
		if s.validateCustomerScope(ctx, state.scope) != nil || part.IsMock || part.ErrorCode != "" {
			return false
		}
		for _, item := range part.Evidence {
			state.stage = "evidence_validation"
			allowed, err := auth.CustomerEvidenceAllowed(ctx, s.DB, state.scope.TenantDbId, state.scope.ShopId, item)
			if err != nil || !allowed || ids[item.Id] || ranks[item.Rank] || item.Rank <= 0 || len(evidence) >= 8 {
				return false
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
	if !complete {
		state.reason = "knowledge_missing"
		return false
	}
	if len(evidence) == 0 {
		if !allowEmpty {
			state.reason = "knowledge_missing"
			return false
		}
		state.evidence = nil
		return true
	}
	state.evidence = evidence
	return true
}

func (s *Service) generateCustomerAnswer(ctx context.Context, state *customerRunState, query string) bool {
	state.stage = "generation"
	state.p.Facts = state.facts
	ctx = observability.PrepareRPC(ctx, "customer", "generate", state.scope.GetRequestId())
	generated, err := s.Rag.Generate(ctx, &ragv1.GenerateRequest{Context: state.scope, Query: query, Evidence: state.evidence, Facts: state.facts, ResponsePurpose: "customer_chat", HistorySummary: state.history})
	if err != nil {
		return false
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
			return false
		}
		if s.validateCustomerScope(ctx, state.scope) != nil || done || part.Sequence != sequence+1 {
			return false
		}
		sequence = part.Sequence
		switch event := part.Event.(type) {
		case *ragv1.GenerateResponse_Delta:
			text.WriteString(event.Delta)
			if text.Len() > 32768 {
				return false
			}
		case *ragv1.GenerateResponse_Decision:
			if state.p.Decision != nil {
				return false
			}
			state.p.Decision = event.Decision
		case *ragv1.GenerateResponse_Citation:
			canonical := false
			for _, item := range state.evidence {
				canonical = canonical || proto.Equal(item, event.Citation)
			}
			if !canonical {
				return false
			}
			state.p.Evidence = append(state.p.Evidence, event.Citation)
		case *ragv1.GenerateResponse_IsMock:
			if profileSeen {
				return false
			}
			profileSeen = true
			state.p.Mock = event.IsMock
		case *ragv1.GenerateResponse_ErrorCode:
			return false
		case *ragv1.GenerateResponse_Done:
			if !event.Done {
				return false
			}
			done = true
		}
	}
	state.p.Answer = text.String()
	if !done || !profileSeen || ValidateProposal(state.p) != nil {
		return false
	}
	return true
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
	if err != nil || revision != scope.PermissionRevision || scope.ConversationEpoch != uint64(c.Epoch) || scope.ScopeExpiresAt <= time.Now().Unix() ||
		c.ServiceType != scope.ServiceType || c.OrderID != scope.CustomerOrderId {
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
	if reason == "" && p.Decision != nil && shouldRevalidateFacts(p.Decision.Behavior, p.Facts) {
		reason = s.revalidateFacts(ctx, scope, p.Facts)
	}
	return reason, nil
}

// shouldRevalidateFacts limits the commit-time fact check to ANSWER. A CLARIFY
// or HANDOFF does not rely on facts, so a provider outage must not turn it into
// a failure.
func shouldRevalidateFacts(behavior ragv1.ReplyBehavior, facts []*ragv1.Fact) bool {
	return behavior == ragv1.ReplyBehavior_REPLY_BEHAVIOR_ANSWER && len(facts) > 0
}

// revalidateFacts re-runs the authorized lookup at commit time. Values may have
// changed since generation; the answer keeps its own observed_at, so only the
// binding and the caller's authorization are rechecked.
func (s *Service) revalidateFacts(ctx context.Context, scope *ragv1.RequestContext, items []*ragv1.Fact) string {
	provider := s.Facts
	if provider == nil {
		provider = facts.UnavailableProvider{}
	}
	groups := map[string][]string{}
	for _, fact := range items {
		if !validCustomerFact(fact) {
			return "permission_changed"
		}
		if field := customerFactTools[fact.ToolName]; field == "order_id" {
			if fact.ResourceId != scope.CustomerOrderId {
				return "permission_changed"
			}
		} else if !skuPattern.MatchString(fact.ResourceId) {
			return "permission_changed"
		}
		groups[fact.ResourceId] = append(groups[fact.ResourceId], fact.ToolName)
	}
	for resource, names := range groups {
		if _, err := facts.Lookup(ctx, provider, scope, names, resource); err != nil {
			return facts.FailureCode(err)
		}
	}
	return ""
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
	raw, err := marshalCitationSnapshot(p.Evidence)
	if err != nil {
		return err
	}
	factsRaw := []byte("[]")
	if len(p.Facts) > 0 {
		if factsRaw, err = json.Marshal(p.Facts); err != nil {
			return err
		}
	}
	if err = completeCustomerExecution(ctx, tx, execution, executionState, behavior.String(), p.Decision.ReasonCode, p.Answer, raw, factsRaw, p.Mock); err != nil {
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
