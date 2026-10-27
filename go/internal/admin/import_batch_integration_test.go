// Provider profile mock is intentional: this test verifies the import batch
// flow and database state; real provider capability is proven by the lifecycle run.
package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func integrationValues(t *testing.T) map[string]string {
	t.Helper()
	path := os.Getenv("V2_INTEGRATION_ENV")
	if path == "" {
		t.Skip("V2_INTEGRATION_ENV is required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("owned regular 0600 integration configuration required")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		t.Fatal("integration configuration owner mismatch")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = strings.Trim(value, "\"'")
		}
	}
	return values
}

func integrationHandler(t *testing.T) (http.Handler, *pgxpool.Pool, *pgxpool.Pool, string) {
	t.Helper()
	values := integrationValues(t)
	ctx := context.Background()
	migration, err := pgxpool.New(ctx, values["V2_MIGRATION_DSN"])
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := pgxpool.New(ctx, values["V2_GATEWAY_DSN"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(migration.Close)
	t.Cleanup(gateway.Close)
	alias := fmt.Sprintf("import-batch-%d", time.Now().UnixNano())
	var tenant, user int64
	if err = migration.QueryRow(ctx, `INSERT INTO tenant(name,external_id) VALUES($1,$1) RETURNING id`, alias).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Exec(ctx, `INSERT INTO shop(tenant_id,id,name) VALUES($1,'shop-import','Import')`, tenant); err != nil {
		t.Fatal(err)
	}
	if err = migration.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids) VALUES($1,'import-manager','admin',ARRAY['shop-import']) RETURNING id`, tenant).Scan(&user); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"merchant_admin", "knowledge_reviewer"} {
		if _, err = migration.Exec(ctx, `INSERT INTO admin_role_grant(tenant_id,user_id,role,scope_kind,shop_ids,granted_by) VALUES($1,$2,$3,'shops',ARRAY['shop-import'],$2)`, tenant, user, role); err != nil {
			t.Fatal(err)
		}
	}
	var revision string
	if err = migration.QueryRow(ctx, `SELECT permission_revision FROM app_user WHERE id=$1`, user).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	session := &auth.Session{TenantID: tenant, UserID: user, Tenant: alias, User: "import-manager", Role: "admin",
		Shop: "shop-import", AllowedShops: []string{"shop-import"}, PermissionRevision: revision}
	key, err := os.ReadFile("/etc/ecommerce-rag/eval-scope.key")
	if err != nil || len(key) < 32 {
		t.Fatal("scope signing key unavailable")
	}
	authority := &auth.Authority{DB: gateway, Session: session, SigningKey: key, RevisionMu: &sync.RWMutex{}}
	conn, err := grpc.NewClient(values["V2_INGEST_GRPC_ADDR"], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	handler := &Handler{DB: gateway, Session: session, Authority: authority, Ingest: ragv1.NewIngestServiceClient(conn),
		Profile: "v2_langchain_mock", SigningKey: key}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return mux, migration, gateway, alias
}

func apiJSON(t *testing.T, api http.Handler, method, path string, body any, expected int) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	var result map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	if recorder.Code != expected {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, recorder.Code, expected, recorder.Body.String())
	}
	return result
}

func uploadBatch(t *testing.T, api http.Handler, root, manifestText string) map[string]any {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("shop_id", "shop-import")
	_ = writer.WriteField("business_date", "2026-10-27")
	_ = writer.WriteField("manifest", manifestText)
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		_ = writer.WriteField("paths", entry.Name())
		part, _ := writer.CreateFormFile("files", entry.Name())
		content, _ := os.ReadFile(filepath.Join(root, entry.Name()))
		_, _ = part.Write(content)
	}
	_ = writer.Close()
	request := httptest.NewRequest("POST", "/admin/v1/merchant/imports", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	var result map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	if recorder.Code != 201 {
		t.Fatalf("create batch = %d: %s", recorder.Code, recorder.Body.String())
	}
	return result
}

func TestImportBatchLifecycleIntegration(t *testing.T) {
	api, migration, gateway, alias := integrationHandler(t)
	root := filepath.Join("..", "..", "..", "tests", "fixtures", "import-batch-20")
	duplicateContent, _ := os.ReadFile(filepath.Join(root, "doc-03.md"))
	duplicateHash := sha256.Sum256(duplicateContent)
	var tenantID int64
	if err := migration.QueryRow(context.Background(), `SELECT id FROM tenant WHERE external_id=$1`, alias).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	var seedSource, seedDocument int64
	if err := migration.QueryRow(context.Background(), `INSERT INTO source(tenant_id,shop_id,type) VALUES($1,'shop-import','admin_manifest') RETURNING id`, tenantID).Scan(&seedSource); err != nil {
		t.Fatal(err)
	}
	if err := migration.QueryRow(context.Background(), `INSERT INTO document(tenant_id,source_id,logical_key,title) VALUES($1,$2,'seed-duplicate','Seed') RETURNING id`, tenantID, seedSource).Scan(&seedDocument); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.Exec(context.Background(), `INSERT INTO document_version(tenant_id,document_id,source_hash,parser_version,chunk_rule_version,embedding_model,pipeline_version,object_key,status,disclosure_class,review_status)
		VALUES($1,$2,$3,'seed','seed','seed','seed','seed','ready','internal_only','pending')`, tenantID, seedDocument, hex.EncodeToString(duplicateHash[:])); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	apiJSON(t, api, "PUT", "/admin/v1/merchant/agent-review", map[string]any{"enabled": true, "policy_version": "v1", "confirm": true, "reason": "integration test switch"}, 200)
	created := uploadBatch(t, api, root, string(manifestBytes))
	batchID := created["batch_id"].(string)
	rows, err := migration.Query(context.Background(), `SELECT precheck_result FROM import_item WHERE batch_id=$1`, batchID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			continue
		}
		var issues []struct {
			IssueCode string `json:"issue_code"`
		}
		_ = json.Unmarshal(raw, &issues)
		for _, issue := range issues {
			counts[issue.IssueCode]++
		}
	}
	rows.Close()
	for _, code := range []string{"missing_source", "expired_content", "duplicate_import", "missing_disclosure_basis"} {
		if counts[code] != 1 {
			t.Fatalf("%s count=%d all=%v", code, counts[code], counts)
		}
	}
	confirmed := apiJSON(t, api, "POST", "/admin/v1/merchant/imports/"+batchID+"/confirm", map[string]any{"confirm": true, "reason": "integration test confirm"}, 202)
	_ = confirmed
	var pending, nullReviews, manifestCount int
	deadline := time.Now().Add(40 * time.Second)
	for {
		err = migration.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE v.review_status='pending'),
			count(*) FILTER (WHERE v.review_status='pending' AND v.reviewed_by IS NULL AND v.reviewed_at IS NULL AND v.reviewed_hash IS NULL),
			count(DISTINCT v.source_manifest_id)
			FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
			WHERE v.tenant_id=(SELECT tenant_id FROM import_batch WHERE id=$1) AND d.logical_key LIKE 'import-doc-%'`, batchID).Scan(&pending, &nullReviews, &manifestCount)
		if err == nil && pending == 20 && nullReviews == 20 && manifestCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending=%d nullReviews=%d manifests=%d err=%v", pending, nullReviews, manifestCount, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	var versionFive, versionSix int64
	var hashFive, hashSix string
	if err = migration.QueryRow(context.Background(), `SELECT v.id,v.source_hash FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id WHERE d.logical_key='import-doc-05' ORDER BY v.id DESC LIMIT 1`).Scan(&versionFive, &hashFive); err != nil {
		t.Fatal(err)
	}
	if err = migration.QueryRow(context.Background(), `SELECT v.id,v.source_hash FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id WHERE d.logical_key='import-doc-06' ORDER BY v.id DESC LIMIT 1`).Scan(&versionSix, &hashSix); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"import-doc-05", "import-doc-06"} {
		if _, err = migration.Exec(context.Background(), `UPDATE document SET meta_json=jsonb_set(meta_json,'{authorization}', '{"basis":"license","reference":"ref","checked_at":"2026-10-01"}'::jsonb) WHERE logical_key=$1`, key); err != nil {
			t.Fatal(err)
		}
	}
	first := apiJSON(t, api, "POST", fmt.Sprintf("/admin/v1/merchant/versions/%d/suggestion", versionFive), map[string]any{"business_date": "2026-10-27"}, 201)
	again := apiJSON(t, api, "POST", fmt.Sprintf("/admin/v1/merchant/versions/%d/suggestion", versionFive), map[string]any{"business_date": "2026-10-27"}, 201)
	if first["suggestion_id"] != again["suggestion_id"] {
		t.Fatalf("suggestion is not idempotent: %v %v", first["suggestion_id"], again["suggestion_id"])
	}
	var suggestionCount int
	_ = migration.QueryRow(context.Background(), `SELECT count(*) FROM review_suggestion WHERE version_id=$1`, versionFive).Scan(&suggestionCount)
	if suggestionCount != 1 {
		t.Fatalf("suggestion rows=%d", suggestionCount)
	}
	six := apiJSON(t, api, "POST", fmt.Sprintf("/admin/v1/merchant/versions/%d/suggestion", versionSix), map[string]any{"business_date": "2026-10-27"}, 201)
	if _, err = migration.Exec(context.Background(), `UPDATE document_version SET source_hash=repeat('f',64) WHERE id=$1`, versionSix); err != nil {
		t.Fatal(err)
	}
	batch := apiJSON(t, api, "POST", "/admin/v1/merchant/versions/batch-approve", map[string]any{
		"items": []map[string]string{{"version_id": strconv.FormatInt(versionFive, 10), "suggestion_id": first["suggestion_id"].(string)},
			{"version_id": strconv.FormatInt(versionSix, 10), "suggestion_id": six["suggestion_id"].(string)}},
		"confirm": true, "reason": "integration test batch approve"}, 200)
	results, _ := batch["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("batch results=%v", batch)
	}
	seenOK, seenStale := false, false
	for _, raw := range results {
		row, _ := raw.(map[string]any)
		if row["ok"] == true {
			seenOK = true
		}
		if row["code"] == "suggestion_stale" {
			seenStale = true
		}
	}
	if !seenOK || !seenStale {
		t.Fatalf("partial results=%v", results)
	}
	var reviewedBy *int64
	var reviewedHash *string
	_ = migration.QueryRow(context.Background(), `SELECT reviewed_by,reviewed_hash FROM document_version WHERE id=$1`, versionFive).Scan(&reviewedBy, &reviewedHash)
	if reviewedBy == nil || reviewedHash == nil {
		t.Fatal("merchant review did not write reviewed_*")
	}
	if _, err = migration.Exec(context.Background(), `UPDATE document_version SET source_hash=$1 WHERE id=$2`, hashFive, versionFive); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Exec(context.Background(), `UPDATE document_version SET source_hash=$1 WHERE id=$2`, hashSix, versionSix); err != nil {
		t.Fatal(err)
	}
	apiJSON(t, api, "POST", "/admin/v1/merchant/imports/"+batchID+"/revoke", map[string]any{"confirm": true, "reason": "integration test revoke"}, 200)
	var documents, versions int
	for {
		if err = migration.QueryRow(context.Background(), `SELECT count(*) FROM document d
			WHERE d.tenant_id=(SELECT tenant_id FROM import_batch WHERE id=$1) AND d.logical_key LIKE 'import-doc-%'`, batchID).Scan(&documents); err != nil {
			t.Fatal(err)
		}
		if err = migration.QueryRow(context.Background(), `SELECT count(*) FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
			WHERE d.tenant_id=(SELECT tenant_id FROM import_batch WHERE id=$1) AND d.logical_key LIKE 'import-doc-%' AND v.status='revoked'`, batchID).Scan(&versions); err != nil {
			t.Fatal(err)
		}
		if documents == 20 && versions == 20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("documents=%d revokedVersions=%d", documents, versions)
		}
		time.Sleep(500 * time.Millisecond)
	}
	var audits int
	if err = migration.QueryRow(context.Background(), `SELECT count(*) FROM admin_audit WHERE resource_type='import_batch' AND resource_id=$1 AND action IN ('import.create','import.revoke')`, batchID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	// Expired staging is cleaned when a new batch arrives.
	var tenant int64
	_ = migration.QueryRow(context.Background(), `SELECT tenant_id FROM import_batch WHERE id=$1`, batchID).Scan(&tenant)
	temp, _ := os.CreateTemp("", "expired-import-*")
	_ = temp.Close()
	var expiredID int64
	if err = migration.QueryRow(context.Background(), `INSERT INTO import_batch(tenant_id,shop_id,created_by,status,business_date,expires_at,agent_rules_version,agent_model_id,manifest_yaml,manifest_sha256)
		VALUES($1,'shop-import',(SELECT created_by FROM import_batch WHERE id=$2),'staged','2026-10-27',now()-interval '1 hour','v1','local-rules-v1','x',$3) RETURNING id`,
		tenant, batchID, strings.Repeat("0", 64)).Scan(&expiredID); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Exec(context.Background(), `INSERT INTO import_item(batch_id,source_hash,temp_path) VALUES($1,$2,$3)`, expiredID, strings.Repeat("1", 64), temp.Name()); err != nil {
		t.Fatal(err)
	}
	uploadBatch(t, api, root, string(manifestBytes))
	var expiredStatus string
	_ = migration.QueryRow(context.Background(), `SELECT status FROM import_batch WHERE id=$1`, expiredID).Scan(&expiredStatus)
	if expiredStatus != "expired" {
		t.Fatalf("expired status=%s", expiredStatus)
	}
	if _, err = os.Stat(temp.Name()); !os.IsNotExist(err) {
		t.Fatal("expired temp file still exists")
	}
	// GET returns the latest append-only switch row.
	apiJSON(t, api, "PUT", "/admin/v1/merchant/agent-review", map[string]any{"enabled": false, "policy_version": "v1", "confirm": true, "reason": "integration test switch latest"}, 200)
	if got := apiJSON(t, api, "GET", "/admin/v1/merchant/agent-review", nil, 200); got["enabled"] != false {
		t.Fatalf("latest switch=%v", got["enabled"])
	}
	_ = gateway
	_ = sha256.Sum256
	_ = hex.EncodeToString
}
