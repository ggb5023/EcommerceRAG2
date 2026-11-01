//go:build integration

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// dataset_sha256 is nullable; reading it into a plain string made the job detail
// endpoint fail with a scan error. This pins the NULL case for both layers.
func TestJobDetailAcceptsNullDatasetHash(t *testing.T) {
	api, migration, _, alias, _ := integrationHandlerWithIngest(t, false)
	ctx := context.Background()

	var tenant int64
	if err := migration.QueryRow(ctx, `SELECT id FROM tenant WHERE external_id=$1`, alias).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	var source int64
	if err := migration.QueryRow(ctx, `INSERT INTO source(tenant_id,shop_id,type) VALUES($1,'shop-import','admin_manifest') RETURNING id`, tenant).Scan(&source); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := migration.QueryRow(ctx, `INSERT INTO ingest_job(tenant_id,source_id,idempotency_key,stage,status,manifest_sha256)
		VALUES($1,$2,$3,'queued','running','aa') RETURNING id`, tenant, source, "null-dataset-"+time.Now().Format("150405.000000")).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(ctx, `UPDATE ingest_job SET dataset_sha256=NULL WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}

	var store Store
	detail, err := store.JobDetail(ctx, migration, tenant, jobID, []string{"shop-import"})
	if err != nil {
		t.Fatalf("JobDetail with a NULL dataset_sha256: %v", err)
	}
	if detail.DatasetHash != nil {
		t.Fatalf("DatasetHash = %v, want nil", *detail.DatasetHash)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/admin/v1/merchant/ingestion/"+strconv.FormatInt(jobID, 10), nil)
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	value, present := payload["dataset_sha256"]
	if !present {
		t.Fatal("dataset_sha256 key is missing; it must be present and null")
	}
	if value != nil {
		t.Fatalf("dataset_sha256 = %v, want null", value)
	}
}

// Jobs scans the same nullable column as JobDetail. A plain string caused the
// list endpoint to fail on historical rows created before dataset_sha256 was
// populated.
func TestJobRowAcceptsNullDatasetHash(t *testing.T) {
	api, migration, _, alias, _ := integrationHandlerWithIngest(t, false)
	ctx := context.Background()

	var tenant int64
	if err := migration.QueryRow(ctx, `SELECT id FROM tenant WHERE external_id=$1`, alias).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	var source int64
	if err := migration.QueryRow(ctx, `INSERT INTO source(tenant_id,shop_id,type) VALUES($1,'shop-import','admin_manifest') RETURNING id`, tenant).Scan(&source); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := migration.QueryRow(ctx, `INSERT INTO ingest_job(tenant_id,source_id,idempotency_key,stage,status,manifest_sha256)
		VALUES($1,$2,$3,'queued','running','aa') RETURNING id`, tenant, source, "null-list-dataset-"+time.Now().Format("150405.000000")).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(ctx, `UPDATE ingest_job SET dataset_sha256=NULL WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}

	var store Store
	jobs, err := store.Jobs(ctx, migration, tenant, []string{"shop-import"})
	if err != nil {
		t.Fatalf("Jobs with a NULL dataset_sha256: %v", err)
	}
	found := false
	for _, job := range jobs {
		if job.ID != jobID {
			continue
		}
		found = true
		if job.DatasetHash != nil {
			t.Fatalf("DatasetHash = %v, want nil", *job.DatasetHash)
		}
	}
	if !found {
		t.Fatalf("job %d was not returned by Jobs", jobID)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/admin/v1/merchant/ingestion", nil)
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, item := range payload.Items {
		if item["job_id"] != strconv.FormatInt(jobID, 10) {
			continue
		}
		value, present := item["dataset_sha256"]
		if !present {
			t.Fatal("dataset_sha256 key is missing; it must be present and null")
		}
		if value != nil {
			t.Fatalf("dataset_sha256 = %v, want null", value)
		}
		return
	}
	t.Fatalf("job %d was not returned by the ingestion endpoint", jobID)
}
