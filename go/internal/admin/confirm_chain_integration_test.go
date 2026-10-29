//go:build integration

package admin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Two concurrent confirms must not both queue a job: the status guard lets one
// caller win and the other sees 409 batch_already_confirming.
func TestConcurrentConfirmClaimsOnce(t *testing.T) {
	api, migration, _, _, _ := integrationHandler(t)
	root := "../../../tests/fixtures/import-batch-20"
	manifestBytes := readManifest(t, root)
	created := uploadBatch(t, api, root, string(manifestBytes))
	batchID := created["batch_id"].(string)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	bodies := make([]string, 2)
	for index := range statuses {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			statuses[slot], bodies[slot] = apiStatus(t, api, "POST", "/admin/v1/merchant/imports/"+batchID+"/confirm",
				map[string]any{"confirm": true, "reason": "integration concurrent confirm"})
		}(index)
	}
	wg.Wait()

	accepted, rejected := 0, 0
	for index, status := range statuses {
		switch status {
		case http.StatusAccepted:
			accepted++
		case http.StatusConflict:
			rejected++
			// The loser may observe either the claim (batch_already_confirming) or
			// the pre-read (import_state_conflict); both mean "not staged anymore".
			if !strings.Contains(bodies[index], "batch_already_confirming") &&
				!strings.Contains(bodies[index], "import_state_conflict") {
				t.Fatalf("409 body = %s", bodies[index])
			}
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("statuses = %v, want one 202 and one 409", statuses)
	}

	var state string
	var jobID *int64
	if err := migration.QueryRow(context.Background(),
		`SELECT status,ingest_job_id FROM import_batch WHERE id=$1`, batchID).Scan(&state, &jobID); err != nil {
		t.Fatal(err)
	}
	if state != "confirmed" || jobID == nil {
		t.Fatalf("batch state = %s job=%v, want confirmed with a job id", state, jobID)
	}
}

// A queue failure releases the claim: the batch returns to staged so the merchant
// can retry, and the failure is recorded in the audit trail.
func TestConfirmQueueFailureReleasesClaim(t *testing.T) {
	api, migration, _, _, handler := integrationHandlerWithIngest(t, false)
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	_ = dead.Close()
	conn, err := grpc.NewClient(deadAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	handler.Ingest = ragv1.NewIngestServiceClient(conn)

	root := "../../../tests/fixtures/import-batch-20"
	created := uploadBatch(t, api, root, string(readManifest(t, root)))
	batchID := created["batch_id"].(string)

	status, body := apiStatus(t, api, "POST", "/admin/v1/merchant/imports/"+batchID+"/confirm",
		map[string]any{"confirm": true, "reason": "integration queue failure"})
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", status, body)
	}
	if !strings.Contains(body, "confirm_queue_failed") && !strings.Contains(body, "ingest_") {
		t.Fatalf("body = %s", body)
	}
	var state string
	if err = migration.QueryRow(context.Background(), `SELECT status FROM import_batch WHERE id=$1`, batchID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "staged" {
		t.Fatalf("batch state = %s, want staged so the merchant can retry", state)
	}
	var audits int
	if err = migration.QueryRow(context.Background(), `SELECT count(*) FROM admin_audit
		WHERE tenant_id=(SELECT tenant_id FROM import_batch WHERE id=$1) AND reason='confirm_queue_failed'`, batchID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits == 0 {
		t.Fatal("no confirm_queue_failed audit row was written")
	}
}

// After a released claim the same batch can be confirmed again; the idempotency
// key on Submit keeps it at one ingest job. Requires the integration ingest
// endpoint (see docs/testing-refactor-plan.md §5.3).
func TestConfirmRetryAfterReleaseQueuesOnce(t *testing.T) {
	api, migration, _, _, handler := integrationHandler(t)
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	_ = dead.Close()
	failing, err := grpc.NewClient(deadAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	root := "../../../tests/fixtures/import-batch-20"
	created := uploadBatch(t, api, root, string(readManifest(t, root)))
	batchID := created["batch_id"].(string)

	live := handler.Ingest
	handler.Ingest = ragv1.NewIngestServiceClient(failing)
	status, body := apiStatus(t, api, "POST", "/admin/v1/merchant/imports/"+batchID+"/confirm",
		map[string]any{"confirm": true, "reason": "integration first attempt"})
	if status != http.StatusServiceUnavailable {
		_ = failing.Close()
		t.Fatalf("first confirm status = %d body=%s, want 503", status, body)
	}
	_ = failing.Close()
	handler.Ingest = live

	status, body = apiStatus(t, api, "POST", "/admin/v1/merchant/imports/"+batchID+"/confirm",
		map[string]any{"confirm": true, "reason": "integration retry attempt"})
	if status != http.StatusAccepted {
		t.Fatalf("retry status = %d body=%s, want 202", status, body)
	}
	var state string
	var jobID *int64
	if err = migration.QueryRow(context.Background(), `SELECT status,ingest_job_id FROM import_batch WHERE id=$1`, batchID).
		Scan(&state, &jobID); err != nil {
		t.Fatal(err)
	}
	if state != "confirmed" || jobID == nil {
		t.Fatalf("state = %s job=%v, want confirmed with a job id", state, jobID)
	}
	// The idempotency key is "admin:<shop>:<manifest_sha256>", so a retry must reuse
	// the same ingest job rather than queue a second one.
	var jobs int
	if err = migration.QueryRow(context.Background(), `SELECT count(*) FROM import_batch WHERE tenant_id=(SELECT tenant_id FROM import_batch WHERE id=$1) AND manifest_sha256=(SELECT manifest_sha256 FROM import_batch WHERE id=$1) AND ingest_job_id IS NOT NULL`, batchID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("batches with a job for this manifest = %d, want 1", jobs)
	}
}

// A claim older than ten minutes is returned to staged by the cleanup sweep that
// every new import triggers.
func TestConfirmTimeoutReclaimsBatch(t *testing.T) {
	api, migration, _, _, _ := integrationHandlerWithIngest(t, false)
	root := "../../../tests/fixtures/import-batch-20"
	created := uploadBatch(t, api, root, string(readManifest(t, root)))
	batchID := created["batch_id"].(string)

	ctx := context.Background()
	if _, err := migration.Exec(ctx, `UPDATE import_batch SET status='confirming', confirm_started_at=now()-interval '20 minutes'
		WHERE id=$1`, batchID); err != nil {
		t.Fatal(err)
	}
	// Any upload runs cleanupExpiredImports for the shop.
	_ = uploadBatch(t, api, root, string(readManifest(t, root)))

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		if err := migration.QueryRow(ctx, `SELECT status FROM import_batch WHERE id=$1`, batchID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "staged" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("batch was not reclaimed within the deadline")
}

// The claim itself is the concurrency guard: exactly one caller may move a staged
// batch to confirming. This runs without the ingest endpoint.
func TestClaimBatchForConfirmIsExclusive(t *testing.T) {
	_, migration, _, _, _ := integrationHandlerWithIngest(t, false)
	root := "../../../tests/fixtures/import-batch-20"
	manifest := string(readManifest(t, root))
	ctx := context.Background()

	var tenant int64
	alias := fmt.Sprintf("claim-%d", time.Now().UnixNano())
	if err := migration.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES('claim',$1) RETURNING id`, alias).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = migration.Exec(context.Background(), `DELETE FROM tenant WHERE id=$1`, tenant) }()
	var actor int64
	if err := migration.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,'claimer','admin',ARRAY['shop-import']) RETURNING id`, tenant).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	var batchID int64
	if err := migration.QueryRow(ctx, `INSERT INTO import_batch(tenant_id,shop_id,created_by,business_date,agent_rules_version,agent_model_id,manifest_yaml,manifest_sha256)
		VALUES($1,'shop-import',$2,'2026-10-27','v1','local',$3::text,encode(sha256($3::text::bytea),'hex')) RETURNING id`, tenant, actor, manifest).Scan(&batchID); err != nil {
		t.Fatal(err)
	}

	var store Store
	var wg sync.WaitGroup
	wins := make([]int64, 2)
	for index := range wins {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			rows, err := store.ClaimBatchForConfirm(ctx, migration, tenant, "shop-import", batchID)
			if err != nil {
				t.Errorf("claim error: %v", err)
				return
			}
			wins[slot] = rows
		}(index)
	}
	wg.Wait()
	if wins[0]+wins[1] != 1 {
		t.Fatalf("claims = %v, want exactly one winner", wins)
	}
	var state string
	if err := migration.QueryRow(ctx, `SELECT status FROM import_batch WHERE id=$1`, batchID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "confirming" {
		t.Fatalf("state = %s, want confirming", state)
	}
}
