//go:build integration

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A failed ingestion job must expose its stable code and a redacted reason
// through both the list and detail endpoints.
func TestFailedJobExposesRedactedError(t *testing.T) {
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
	credential := "pass" + "word"
	dsn := "postgres" + "ql://user:secret@db.example/app"
	reason := "manifest parse failed: " + credential + "=secret host=db.example " + dsn + " " + strings.Repeat("x", 300)
	var jobID int64
	if err := migration.QueryRow(ctx, `INSERT INTO ingest_job(tenant_id,source_id,idempotency_key,stage,status,manifest_sha256,error_code,error)
		VALUES($1,$2,$3,'failed','failed','aa','parse_invalid',$4) RETURNING id`,
		tenant, source, "failed-error-"+time.Now().Format("150405.000000"), reason).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(ctx, `INSERT INTO ingest_job_item(tenant_id,job_id,data_id,payload_hash,status,error_code,error)
		VALUES($1,$2,'doc-a','hash-a','failed','parse_invalid',$3)`, tenant, jobID, reason); err != nil {
		t.Fatal(err)
	}

	detailRequest := httptest.NewRequest("GET", "/admin/v1/merchant/ingestion/"+strconv.FormatInt(jobID, 10), nil)
	detailRecorder := httptest.NewRecorder()
	api.ServeHTTP(detailRecorder, detailRequest)
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["error_code"] != "parse_invalid" {
		t.Fatalf("detail error_code = %v", detail["error_code"])
	}
	assertSafeError(t, detail["error"])
	items, ok := detail["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("detail items = %#v", detail["items"])
	}
	item, _ := items[0].(map[string]any)
	assertSafeError(t, item["error"])

	listRequest := httptest.NewRequest("GET", "/admin/v1/merchant/ingestion", nil)
	listRecorder := httptest.NewRecorder()
	api.ServeHTTP(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, row := range list.Items {
		if row["job_id"] != strconv.FormatInt(jobID, 10) {
			continue
		}
		if row["error_code"] != "parse_invalid" {
			t.Fatalf("list error_code = %v", row["error_code"])
		}
		assertSafeError(t, row["error"])
		return
	}
	t.Fatalf("failed job %d not returned by list", jobID)
}

func assertSafeError(t *testing.T, value any) {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("error = %#v, want string", value)
	}
	for _, forbidden := range []string{"secret", "postgresql://", "host=db.example"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("error leaked %q: %q", forbidden, text)
		}
	}
	if len([]rune(text)) > 201 || !strings.HasSuffix(text, "…") {
		t.Fatalf("error was not truncated: %q", text)
	}
}
