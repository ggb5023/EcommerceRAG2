//go:build integration

package localauth

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationDSNs(t *testing.T) (migrator, gateway string) {
	t.Helper()
	path := os.Getenv("V2_INTEGRATION_ENV")
	if path == "" {
		t.Skip("V2_INTEGRATION_ENV is required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("integration env must be an owned 0600 file")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(mustReadFile(t, path)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "\"'")
		}
	}
	if values["V2_MIGRATION_DSN"] == "" || values["V2_GATEWAY_DSN"] == "" {
		t.Fatalf("integration env needs V2_MIGRATION_DSN and V2_GATEWAY_DSN")
	}
	return values["V2_MIGRATION_DSN"], values["V2_GATEWAY_DSN"]
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read integration env")
	}
	return data
}

type fixture struct {
	tenantID   int64
	ownerID    int64
	operatorID int64
	tenantName string
}

// newFixture creates one tenant with a shop. Owner skeleton and operator
// credential are added per test so each case controls its own setup state.
func newFixture(t *testing.T, migrator, gateway string, withOwner bool) (*Store, fixture) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, migrator)
	if err != nil {
		t.Fatalf("migrator pool: %v", err)
	}
	defer pool.Close()
	suffix := strings.ToLower(strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
	f := fixture{tenantName: "it-" + suffix}
	if err := pool.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES($1,$2) RETURNING id`,
		"integration", f.tenantName).Scan(&f.tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO shop(tenant_id,id,name,public_slug,customer_chat_enabled)
		VALUES($1,'main-shop','Integration shop',$2,false)`, f.tenantID, "it-"+suffix); err != nil {
		t.Fatalf("insert shop: %v", err)
	}
	if withOwner {
		if err := pool.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids)
			VALUES($1,'owner','owner',ARRAY['main-shop']) RETURNING id`, f.tenantID).Scan(&f.ownerID); err != nil {
			t.Fatalf("insert owner: %v", err)
		}
	}
	gatewayPool, err := pgxpool.New(ctx, gateway)
	if err != nil {
		t.Fatalf("gateway pool: %v", err)
	}
	t.Cleanup(gatewayPool.Close)
	store := NewStore(gatewayPool)
	store.Tenant = f.tenantName
	return store, f
}

func addCredential(t *testing.T, store *Store, tenantID, userID int64) {
	t.Helper()
	hash, err := Hash("integration-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(context.Background(), `INSERT INTO local_credential(tenant_id,user_id,password_hash)
		VALUES($1,$2,$3)`, tenantID, userID, hash); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
}

func TestConcurrentCompleteSetupKeepsOneWinner(t *testing.T) {
	migrator, gateway := integrationDSNs(t)
	store, f := newFixture(t, migrator, gateway, true)
	target, err := store.SetupTarget(context.Background())
	if err != nil || target.OwnerUserID != f.ownerID {
		t.Fatalf("setup target: %v %+v", err, target)
	}
	hash, err := Hash("integration-password")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index] = store.CompleteSetup(context.Background(), target, hash)
		}(i)
	}
	wg.Wait()
	success, completed := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrSetupComplete):
			completed++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || completed != 1 {
		t.Fatalf("want one winner and one 409, got success=%d completed=%d", success, completed)
	}
}

func TestSetupTargetRequiresOwnerSkeleton(t *testing.T) {
	migrator, gateway := integrationDSNs(t)
	store, _ := newFixture(t, migrator, gateway, false)
	if _, err := store.SetupTarget(context.Background()); !errors.Is(err, ErrSetupNeedsBootstrap) {
		t.Fatalf("want ErrSetupNeedsBootstrap, got %v", err)
	}
}

func TestSetupTargetRejectsExistingCredential(t *testing.T) {
	migrator, gateway := integrationDSNs(t)
	store, f := newFixture(t, migrator, gateway, true)
	addCredential(t, store, f.tenantID, f.ownerID)
	if _, err := store.SetupTarget(context.Background()); !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("want ErrSetupComplete, got %v", err)
	}
	target := SetupTarget{TenantID: f.tenantID, OwnerUserID: f.ownerID}
	hash, err := Hash("integration-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteSetup(context.Background(), target, hash); !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("CompleteSetup must re-check the gate, got %v", err)
	}
}

func TestDisableRevokesSessionsAndEnableKeepsThemRevoked(t *testing.T) {
	migrator, gateway := integrationDSNs(t)
	store, f := newFixture(t, migrator, gateway, true)
	ctx := context.Background()
	var operatorID int64
	if err := store.DB.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids)
		VALUES($1,'operator','operator',ARRAY['main-shop']) RETURNING id`, f.tenantID).Scan(&operatorID); err != nil {
		t.Fatalf("insert operator: %v", err)
	}
	addCredential(t, store, f.tenantID, operatorID)

	session, cookie, _, err := NewSession(f.tenantID, operatorID, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.ResolveSession(ctx, HashToken(cookie)); err != nil {
		t.Fatalf("session should resolve before disable: %v", err)
	}
	if err := store.DisableUser(ctx, f.tenantID, operatorID, f.ownerID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := store.ResolveSession(ctx, HashToken(cookie)); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("disabled account must report account_revoked, got %v", err)
	}
	if err := store.EnableUser(ctx, f.tenantID, operatorID, f.ownerID); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := store.ResolveSession(ctx, HashToken(cookie)); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("old session must stay revoked after enable, got %v", err)
	}
	var events int
	if err := store.DB.QueryRow(ctx, `SELECT count(*) FROM local_auth_event
		WHERE tenant_id=$1 AND user_id=$2 AND event IN ('account_disabled','account_enabled')`,
		f.tenantID, operatorID).Scan(&events); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if events != 2 {
		t.Fatalf("want two audit events, got %d", events)
	}
}
