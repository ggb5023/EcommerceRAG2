package facts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

var ErrToolNotAllowed = errors.New("fact tool is not allowlisted")
var ErrUnavailable = errors.New("fact provider is unavailable")
var ErrUnauthorized = errors.New("fact provider authorization failed")
var ErrRateLimited = errors.New("fact provider rate limited")
var ErrTimedOut = errors.New("fact provider timed out")
var ErrCanceled = errors.New("fact provider request canceled")
var ErrInvalidResponse = errors.New("fact provider returned an invalid response")

// Provider is deliberately owned by the Go control plane.  A provider
// implementation may call a read-only platform adapter, but Python never gets
// a tool name or credentials that Go has not approved.
type Provider interface {
	Lookup(context.Context, string, *ragv1.RequestContext, string) (*ragv1.Fact, error)
}

type UnavailableProvider struct{}

func (UnavailableProvider) Lookup(context.Context, string, *ragv1.RequestContext, string) (*ragv1.Fact, error) {
	return nil, ErrUnavailable
}

var allowedFactTools = map[string]struct{}{
	"get_stock":        {},
	"get_price":        {},
	"get_order_status": {},
	"get_logistics":    {},
	"compare_products": {},
}

func requiresResourceBinding(tool string) bool {
	return tool == "get_order_status" || tool == "get_logistics"
}

func ValidateNames(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := allowedFactTools[name]; !ok {
			return fmt.Errorf("%w: %s", ErrToolNotAllowed, name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("fact tool is duplicated: %s", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func FailureCode(err error) string {
	switch {
	case errors.Is(err, ErrToolNotAllowed):
		return "fact_tool_not_allowed"
	case errors.Is(err, ErrUnavailable):
		return "fact_provider_unavailable"
	case errors.Is(err, ErrUnauthorized):
		return "fact_provider_unauthorized"
	case errors.Is(err, ErrRateLimited):
		return "fact_provider_rate_limited"
	case errors.Is(err, ErrTimedOut):
		return "fact_provider_timeout"
	case errors.Is(err, ErrCanceled):
		return "fact_provider_canceled"
	case errors.Is(err, ErrInvalidResponse):
		return "fact_provider_invalid_response"
	default:
		return "fact_tool_failed"
	}
}

func Lookup(ctx context.Context, provider Provider, scope *ragv1.RequestContext, names []string, resourceID string) ([]*ragv1.Fact, error) {
	if err := ValidateNames(names); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	if provider == nil {
		return nil, ErrUnavailable
	}
	for _, name := range names {
		if requiresResourceBinding(name) && (scope == nil || strings.TrimSpace(resourceID) == "") {
			return nil, ErrUnauthorized
		}
	}
	result := make([]*ragv1.Fact, 0, len(names))
	for _, name := range names {
		boundResource := ""
		if requiresResourceBinding(name) {
			boundResource = resourceID
		}
		fact, err := provider.Lookup(ctx, name, scope, boundResource)
		if err != nil {
			return nil, err
		}
		if fact == nil || fact.ToolName != name || fact.ValueJson == "" || fact.ObservedAt == "" {
			return nil, fmt.Errorf("%w: fact tool returned an invalid result: %s", ErrInvalidResponse, name)
		}
		if requiresResourceBinding(name) && fact.ResourceId != resourceID {
			return nil, fmt.Errorf("%w: fact tool returned an unauthorized resource: %s", ErrInvalidResponse, name)
		}
		if !requiresResourceBinding(name) && fact.ResourceId != "" {
			return nil, fmt.Errorf("%w: unexpected resource for %s", ErrInvalidResponse, name)
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(fact.ValueJson), &value); err != nil || value == nil {
			return nil, fmt.Errorf("%w: fact tool returned invalid JSON: %s", ErrInvalidResponse, name)
		}
		if requiresResourceBinding(name) {
			if value["order_id"] != resourceID {
				return nil, fmt.Errorf("%w: fact tool returned a mismatched order: %s", ErrInvalidResponse, name)
			}
		}
		if _, err := time.Parse(time.RFC3339, fact.ObservedAt); err != nil {
			return nil, fmt.Errorf("%w: fact tool returned invalid observed_at: %s", ErrInvalidResponse, name)
		}
		result = append(result, fact)
	}
	return result, nil
}
