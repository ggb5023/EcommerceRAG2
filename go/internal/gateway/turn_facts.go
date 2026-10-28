package gateway

import (
	"context"

	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

// FactClient is the narrow slice of the facts provider this stage needs. It keeps
// the stage testable without a provider and without the gateway struct.
type FactClient interface {
	Lookup(ctx context.Context, scope *ragv1.RequestContext, names []string, resourceID string) ([]*ragv1.Fact, error)
}

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
