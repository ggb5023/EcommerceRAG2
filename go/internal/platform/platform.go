package platform

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

// Platform adapters are read-only at the v2 boundary.  Write operations such
// as refunds or order changes must remain behind the platform's official
// authenticated entrypoint and are intentionally absent from these contracts.
type Product struct {
	SKU          string
	Title        string
	Price        string
	Availability string
}

type Policy struct {
	Key    string
	Text   string
	Source string
}

type ProductSource interface {
	GetProduct(context.Context, *ragv1.RequestContext, string) (Product, error)
}

type PolicySource interface {
	GetPolicy(context.Context, *ragv1.RequestContext, string) (Policy, error)
}

type FactSource interface {
	GetFact(context.Context, *ragv1.RequestContext, string) (*ragv1.Fact, error)
}

type IdentitySource interface {
	ResolveBearer(context.Context, string) (*ragv1.RequestContext, error)
}

var (
	ErrNotConfigured = errors.New("platform adapter is not configured")
	ErrScopeInvalid  = errors.New("platform scope is invalid")
)

func ValidateScope(scope *ragv1.RequestContext) error {
	if scope == nil || scope.TenantId == "" || scope.UserId == "" || scope.ShopId == "" {
		return ErrScopeInvalid
	}
	for _, shop := range scope.AllowedShopIds {
		if shop == scope.ShopId {
			return nil
		}
	}
	return ErrScopeInvalid
}

// ScopedFactSource adapts a platform's read-only fact calls to the Go
// facts.Provider contract. It never accepts tenant/shop values from the query
// and never exposes platform credentials to Python or the browser.
type ScopedFactSource struct {
	Source FactSource
}

func (s ScopedFactSource) Lookup(ctx context.Context, tool string, scope *ragv1.RequestContext) (*ragv1.Fact, error) {
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	if s.Source == nil {
		return nil, ErrNotConfigured
	}
	fact, err := s.Source.GetFact(ctx, scope, tool)
	if err != nil {
		return nil, err
	}
	if fact == nil || fact.ToolName != tool {
		return nil, fmt.Errorf("platform fact tool mismatch: %s", tool)
	}
	return fact, nil
}

func NormalizeKey(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.ContainsAny(value, "/\\: ") {
		return "", errors.New("platform key is invalid")
	}
	return value, nil
}
