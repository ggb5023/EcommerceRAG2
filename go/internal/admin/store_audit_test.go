package admin

import (
	"context"
	"errors"
	"testing"
)

// The store must never open its own transaction: every write joins the caller's.
func TestAppendAuditRequiresTransaction(t *testing.T) {
	var store Store
	err := store.AppendAudit(context.Background(), nil, AuditEntry{Action: "a"})
	if !errors.Is(err, ErrStoreNeedsTransaction) {
		t.Fatalf("err = %v, want ErrStoreNeedsTransaction", err)
	}
}

func TestIssueAdminGrantRequiresTransaction(t *testing.T) {
	var store Store
	allowed, err := store.IssueAdminGrant(context.Background(), nil, GrantRequest{Role: "merchant_admin"})
	if !errors.Is(err, ErrStoreNeedsTransaction) {
		t.Fatalf("err = %v, want ErrStoreNeedsTransaction", err)
	}
	if allowed {
		t.Fatal("a missing transaction must not report an allowed grant")
	}
}
