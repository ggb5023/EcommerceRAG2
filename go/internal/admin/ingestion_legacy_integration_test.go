//go:build integration

package admin

import (
	"context"
	"testing"
)

// A batch-scoped submit must adopt an unfinished job written under the legacy
// shop+manifest key; the dead ingest client proves Submit was not called.
func TestUnfinishedLegacyJobIsReusedWithoutSubmit(t *testing.T) {
	_, migration, _, alias, handler := integrationHandlerWithIngest(t, false)
	ctx := context.Background()
	var tenant int64
	if err := migration.QueryRow(ctx, `SELECT id FROM tenant WHERE external_id=$1`, alias).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	var source int64
	if err := migration.QueryRow(ctx, `INSERT INTO source(tenant_id,shop_id,type)
		VALUES($1,'shop-import','admin_manifest') RETURNING id`, tenant).Scan(&source); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version: 1\nsource:\n  id: docs\n"
	var jobID int64
	if err := migration.QueryRow(ctx, `INSERT INTO ingest_job(tenant_id,source_id,idempotency_key,stage,status,manifest_sha256)
		VALUES($1,$2,$3,'queued','pending','manifest-hash') RETURNING id`,
		tenant, source, legacyIngestIdempotencyKey("shop-import", manifest)).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	for _, dataID := range []string{"doc-a", "doc-b"} {
		if _, err := migration.Exec(ctx, `INSERT INTO ingest_job_item(tenant_id,job_id,data_id,payload_hash,status,stage)
			VALUES($1,$2,$3,'hash','pending','queued')`, tenant, jobID, dataID); err != nil {
			t.Fatal(err)
		}
	}
	queued, err := handler.queuePackage(ctx, manifest, nil, tenant, handler.Session.UserID, "shop-import", 77)
	if err != nil {
		t.Fatalf("queuePackage: %v", err)
	}
	if queued.JobID != jobID || queued.Status != "pending" || queued.ItemCount != 2 || queued.ManifestSHA256 != "manifest-hash" {
		t.Fatalf("reused job = %+v, want id=%d pending items=2", queued, jobID)
	}
}

// A completed legacy job must not absorb a new batch: the new key has to queue
// its own job, which the dead ingest client surfaces as an error here.
func TestCompletedLegacyJobDoesNotAbsorbANewBatch(t *testing.T) {
	_, migration, _, alias, handler := integrationHandlerWithIngest(t, false)
	ctx := context.Background()
	var tenant int64
	if err := migration.QueryRow(ctx, `SELECT id FROM tenant WHERE external_id=$1`, alias).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	var source int64
	if err := migration.QueryRow(ctx, `INSERT INTO source(tenant_id,shop_id,type)
		VALUES($1,'shop-import','admin_manifest') RETURNING id`, tenant).Scan(&source); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version: 1\nsource:\n  id: docs\n"
	if _, err := migration.Exec(ctx, `INSERT INTO ingest_job(tenant_id,source_id,idempotency_key,stage,status,manifest_sha256)
		VALUES($1,$2,$3,'published','done','manifest-hash')`,
		tenant, source, legacyIngestIdempotencyKey("shop-import", manifest)); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.queuePackage(ctx, manifest, nil, tenant, handler.Session.UserID, "shop-import", 78); err == nil {
		t.Fatal("queuePackage reused a completed legacy job instead of submitting a new one")
	}
}
