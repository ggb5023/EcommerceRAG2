package admin

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidAdminRequest(t *testing.T) {
	tests := []struct {
		name   string
		Role   string
		scope  string
		shops  []string
		reason string
		want   bool
	}{
		{name: "tenant scope", Role: adminRoleMerchant, scope: "tenant", reason: "Need to manage merchant knowledge", want: true},
		{name: "shop subset", Role: adminRoleReviewer, scope: "shops", shops: []string{"shop-a"}, reason: "Review submitted knowledge", want: true},
		{name: "unsupported role", Role: adminRoleAccess, scope: "tenant", reason: "Need access", want: false},
		{name: "tenant scope with shops", Role: adminRoleMerchant, scope: "tenant", shops: []string{"shop-a"}, reason: "Need access", want: false},
		{name: "shop outside identity", Role: adminRoleMerchant, scope: "shops", shops: []string{"shop-b"}, reason: "Need access", want: false},
		{name: "duplicate shops", Role: adminRoleMerchant, scope: "shops", shops: []string{"shop-a", "shop-a"}, reason: "Need access", want: false},
		{name: "short reason", Role: adminRoleMerchant, scope: "shops", shops: []string{"shop-a"}, reason: "short", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validAdminRequest(test.Role, test.scope, test.shops, test.reason, []string{"shop-a"}); got != test.want {
				t.Fatalf("validAdminRequest() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAllContained(t *testing.T) {
	if !allContained([]string{"shop-a", "shop-b"}, []string{"shop-b"}) {
		t.Fatal("expected requested shops to be contained")
	}
	if allContained([]string{"shop-a"}, []string{"shop-a", "shop-b"}) {
		t.Fatal("expected out-of-scope shop to be rejected")
	}
}

func TestDatabaseDecisionStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "missing or changed resource", err: pgx.ErrNoRows, want: 409},
		{name: "permission denied", err: status.Error(codes.PermissionDenied, "denied"), want: 403},
		{name: "postgres permission denied", err: &pgconn.PgError{Code: "42501"}, want: 403},
		{name: "concurrent state conflict", err: &pgconn.PgError{Code: "40001"}, want: 409},
		{name: "other database error fails closed", err: errors.New("database failure"), want: 503},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := databaseDecisionStatus(test.err); got != test.want {
				t.Fatalf("databaseDecisionStatus() = %d, want %d", got, test.want)
			}
		})
	}
}
