package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/ggb5023/EcommerceRAG/go/internal/ports"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FactClient is the narrow slice of the facts provider this stage needs. It keeps
// the stage testable without a provider and without the gateway struct.
type FactClient interface {
	Lookup(ctx context.Context, scope *ragv1.RequestContext, names []string, resourceID string) ([]*ragv1.Fact, error)
}

// Compile-time proof that the adapter satisfies the published port.
var _ ports.FactClient = factProvider{}

// factProvider adapts the tool registry to FactClient.
type factProvider struct {
	provider facts.Provider
}

func (p factProvider) Lookup(ctx context.Context, scope *ragv1.RequestContext, names []string, resourceID string) ([]*ragv1.Fact, error) {
	return facts.Lookup(ctx, p.provider, scope, names, resourceID)
}

// factInput is everything the facts stage needs.
type factInput struct {
	ctx        context.Context
	scope      *ragv1.RequestContext
	names      []string
	resourceID string
}

// factsResult carries the authorized fact rows for the two fact events.
type factsResult struct {
	facts []*ragv1.Fact
}

// lookupFacts resolves the authorized fact tools for one turn.
//
// An empty tool list never calls the client: the routing stage already decided
// there is no usable identifier, and the turn falls back to knowledge.
func lookupFacts(client FactClient, input factInput) (factsResult, error) {
	if len(input.names) == 0 {
		return factsResult{}, nil
	}
	rows, err := client.Lookup(input.ctx, input.scope, input.names, input.resourceID)
	if err != nil {
		return factsResult{}, err
	}
	return factsResult{facts: rows}, nil
}

// runFactStage resolves the authorized facts and emits one fact_read event per
// row. Trace writes stay best-effort, exactly as before.
func runFactStage(ctx context.Context, client FactClient, db *pgxpool.Pool, tenantID int64, requestID string,
	tools []string, resourceID string, scope *ragv1.RequestContext,
	authorized func() bool, writeEvent func(string, any) error) (rows []*ragv1.Fact, failure *stageFailure, aborted bool) {
	resolution, err := lookupFacts(client, factInput{ctx: ctx, scope: scope, names: tools, resourceID: resourceID})
	if err != nil {
		for _, toolName := range tools {
			_ = recordToolCall(context.Background(), db, tenantID, requestID, toolName, facts.FailureCode(err), true)
		}
		return nil, &stageFailure{state: "FAILED", code: facts.FailureCode(err)}, false
	}
	for _, fact := range resolution.facts {
		if !authorized() {
			return nil, nil, true
		}
		valueHash := sha256.Sum256([]byte(fact.ValueJson))
		if err := writeEvent("fact_read", map[string]any{
			"type": "fact_read", "tool_name": fact.ToolName, "resource_id": fact.ResourceId,
			"observed_at": fact.ObservedAt, "value_sha256": hex.EncodeToString(valueHash[:]), "is_mock": fact.IsMock,
		}); err != nil {
			return nil, &stageFailure{state: "FAILED", code: "fact_evidence_persistence_failed"}, false
		}
		_ = recordToolCall(ctx, db, tenantID, requestID, fact.ToolName, "ok", false)
	}
	return resolution.facts, nil, false
}
