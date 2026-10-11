package main

import (
	"context"
	"testing"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

type testFactProvider struct{}

func (testFactProvider) Lookup(_ context.Context, name string, _ *ragv1.RequestContext) (*ragv1.Fact, error) {
	return &ragv1.Fact{ToolName: name, ValueJson: `{"value":true}`, ObservedAt: "2026-10-09T00:00:00Z"}, nil
}

func TestLookupFactsRequiresAllowlistedUniqueTools(t *testing.T) {
	scope := &ragv1.RequestContext{TenantId: "tenant", ShopId: "shop"}
	if _, err := lookupFacts(context.Background(), testFactProvider{}, scope, []string{"unknown"}); err == nil || factFailureCode(err) != "fact_tool_not_allowed" {
		t.Fatalf("unknown fact tool did not fail closed: %v", err)
	}
	if _, err := lookupFacts(context.Background(), testFactProvider{}, scope, []string{"get_stock", "get_stock"}); err == nil {
		t.Fatal("duplicate fact tool was accepted")
	}
	facts, err := lookupFacts(context.Background(), testFactProvider{}, scope, []string{"get_stock", "get_price"})
	if err != nil || len(facts) != 2 {
		t.Fatalf("allowlisted facts failed: facts=%#v err=%v", facts, err)
	}
}

func TestLookupFactsDoesNotInventUnavailableResults(t *testing.T) {
	if _, err := lookupFacts(context.Background(), unavailableFactProvider{}, &ragv1.RequestContext{}, []string{"get_stock"}); err == nil || factFailureCode(err) != "fact_provider_unavailable" {
		t.Fatalf("unavailable fact provider did not fail closed: %v", err)
	}
}
