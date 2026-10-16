package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

var errFactToolNotAllowed = errors.New("fact tool is not allowlisted")
var errFactProviderUnavailable = errors.New("fact provider is unavailable")

// FactProvider is deliberately owned by the Go control plane.  A provider
// implementation may call a read-only platform adapter, but Python never gets
// a tool name or credentials that Go has not approved.
type FactProvider interface {
	Lookup(context.Context, string, *ragv1.RequestContext) (*ragv1.Fact, error)
}

type unavailableFactProvider struct{}

func (unavailableFactProvider) Lookup(context.Context, string, *ragv1.RequestContext) (*ragv1.Fact, error) {
	return nil, errFactProviderUnavailable
}

var allowedFactTools = map[string]struct{}{
	"get_stock":        {},
	"get_price":        {},
	"get_order_status": {},
	"compare_products": {},
}

func validateFactToolNames(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := allowedFactTools[name]; !ok {
			return fmt.Errorf("%w: %s", errFactToolNotAllowed, name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("fact tool is duplicated: %s", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func factFailureCode(err error) string {
	switch {
	case errors.Is(err, errFactToolNotAllowed):
		return "fact_tool_not_allowed"
	case errors.Is(err, errFactProviderUnavailable):
		return "fact_provider_unavailable"
	default:
		return "fact_tool_failed"
	}
}

func lookupFacts(ctx context.Context, provider FactProvider, scope *ragv1.RequestContext, names []string) ([]*ragv1.Fact, error) {
	if err := validateFactToolNames(names); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	if provider == nil {
		return nil, errFactProviderUnavailable
	}
	result := make([]*ragv1.Fact, 0, len(names))
	for _, name := range names {
		fact, err := provider.Lookup(ctx, name, scope)
		if err != nil {
			return nil, err
		}
		if fact == nil || fact.ToolName != name || fact.ValueJson == "" || fact.ObservedAt == "" {
			return nil, fmt.Errorf("fact tool returned an invalid result: %s", name)
		}
		var value any
		if err := json.Unmarshal([]byte(fact.ValueJson), &value); err != nil || value == nil {
			return nil, fmt.Errorf("fact tool returned invalid JSON: %s", name)
		}
		if _, err := time.Parse(time.RFC3339, fact.ObservedAt); err != nil {
			return nil, fmt.Errorf("fact tool returned invalid observed_at: %s", name)
		}
		result = append(result, fact)
	}
	return result, nil
}
