package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// TestR6V2IsolatedDatabaseAcceptance is the opt-in cross-component gate for
// the first durable v2 ingestion baseline.  It intentionally uses the
// isolated M1 database and a loopback Python process; it never touches the
// production gateway or calls an online provider.
func TestR6V2IsolatedDatabaseAcceptance(t *testing.T) {
	if os.Getenv("ECR_R6_ACCEPTANCE") != "1" {
		t.Skip("set ECR_R6_ACCEPTANCE=1 with the isolated migration environment")
	}
	appDSN := strings.TrimSpace(os.Getenv("M1_APP_DATABASE_URL"))
	adminDSN := strings.TrimSpace(os.Getenv("M1_MIGRATION_DATABASE_URL"))
	if appDSN == "" || adminDSN == "" {
		t.Fatal("M1_APP_DATABASE_URL and M1_MIGRATION_DATABASE_URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	app, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("open isolated app pool: %v", err)
	}
	defer app.Close()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open isolated migration pool: %v", err)
	}
	defer admin.Close()
	var databaseName string
	if err = app.QueryRow(ctx, "SELECT current_database()").Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(databaseName, "m1_test") {
		t.Fatalf("R6 acceptance must use an isolated database, got %s", databaseName)
	}

	session, err := (v2FixtureIdentityAdapter{db: app}).Resolve(ctx)
	if err != nil {
		t.Fatalf("resolve v2 fixture identity: %v", err)
	}
	prefix := "r6-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	defer cleanupR6Rows(context.Background(), admin, session.tenantID, prefix)

	address := freeLoopbackAddress(t)
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(repo, "python/.venv/bin/python")
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/authorization/validate" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": true})
	}))
	defer authority.Close()
	process := exec.Command(python, "-m", "app.v2_grpc_server")
	process.Dir = repo
	process.Env = append(os.Environ(),
		"PYTHONPATH="+filepath.Join(repo, "python"),
		"APP_ENV=test",
		"RAG_PROFILE=v2_langgraph_fixture",
		"V2_GRPC_ADDR="+address,
		"V2_GRPC_REFLECTION=0",
		"V2_CHECKPOINT_DSN="+appDSN,
		"V2_INGEST_DSN="+appDSN,
		"V2_INGEST_POLL_SECONDS=0.4",
		"AUTHORITY_HTTP_BASE="+authority.URL,
	)
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	if err = process.Start(); err != nil {
		t.Fatalf("start Python v2 process: %v", err)
	}
	defer func() {
		_ = process.Process.Signal(os.Interrupt)
		_, _ = process.Process.Wait()
	}()

	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ingestClient := ragv1.NewIngestServiceClient(connection)
	ragClient := ragv1.NewRagServiceClient(connection)
	requestContext := &ragv1.RequestContext{
		RequestId:            "r6-readiness",
		TenantId:             session.tenant,
		UserId:               session.user,
		AllowedShopIds:       []string{session.shop},
		AllowedDocumentIds:   []string{"r6-readiness"},
		ShopId:               session.shop,
		Role:                 "operator",
		TenantDbId:           session.tenantID,
		UserDbId:             session.userID,
		PermissionRevision:   session.permissionRevision,
		EnforceDocumentScope: true,
	}
	waitPythonReady(t, ctx, ragClient, requestContext)

	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{
		db: app, session: session, signingKey: key, profile: "v2_langgraph_fixture",
		facts: unavailableFactProvider{}, cancels: map[string]context.CancelFunc{},
		rag: ragClient, ingest: ingestClient,
	}
	gateway.ready.Store(true)
	gateway.adminIndexReady.Store(true)
	httpServer := httptest.NewServer(gateway.routes())
	defer httpServer.Close()

	// The same idempotency key and payload returns the original job; changing
	// the package under that key is rejected by the Python-owned writer.
	content1 := []byte("# Shipping\n\nOrders leave within two business days.\n")
	manifest1 := r6Manifest(prefix+"-source", prefix+"-doc", "Shipping", "shipping.md", content1)
	first := submitR6(t, ctx, ingestClient, requestContext, prefix+"-same", manifest1, "shipping.md", content1)
	replay := submitR6(t, ctx, ingestClient, requestContext, prefix+"-same", manifest1, "shipping.md", content1)
	if first.GetJobId() != replay.GetJobId() || first.GetPackageSha256() != replay.GetPackageSha256() {
		t.Fatalf("idempotent replay changed the job: first=%v replay=%v", first, replay)
	}
	contentConflict := []byte("# Shipping\n\nA different package.\n")
	_, err = ingestClient.Submit(ctx, &ragv1.SubmitRequest{
		Context: requestContext, IdempotencyKey: prefix + "-same", ManifestYaml: r6Manifest(prefix+"-source", prefix+"-doc", "Shipping", "shipping.md", contentConflict),
		Files: []*ragv1.PackageFile{{Path: "shipping.md", Content: contentConflict}}, ShopId: session.shop,
		TenantDbId: strconv.FormatInt(session.tenantID, 10), UserDbId: strconv.FormatInt(session.userID, 10),
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("same idempotency key with a different payload was not rejected: %v", err)
	}
	if err = gateway.refreshAuthorization(ctx); err != nil {
		t.Fatalf("refresh authorization after Python submission: %v", err)
	}

	jobID := mustInt64(t, first.JobId)
	job, version := waitR6Reviewable(t, ctx, app, session.tenantID, jobID, prefix+"-doc")
	if job.status != "awaiting_review" || version.status != "ready" || version.reviewStatus != "pending" || version.chunkCount == 0 {
		t.Fatalf("worker did not produce a reviewable version: job=%+v version=%+v", job, version)
	}
	assertR6ReviewBeforeActive(t, ctx, app, session.tenantID, version.id)

	// Go owns review and publication control; Python owns the job/item/version
	// rows.  Review must precede activation, and publish is safe to replay.
	reviewBody := map[string]any{
		"confirm": true, "tenant_id": session.tenant, "shop_id": session.shop,
		"document_id": prefix + "-doc", "expected_hash": version.sourceHash,
		"permission_revision": session.permissionRevision, "expected_fencing_epoch": version.fencing,
		"decision": "approved", "disclosure_class": "external_allowed", "external_allowed": true,
		"reason": "R6 isolated review acceptance",
	}
	reviewBody["permission_revision"] = gateway.currentPermissionRevision()
	statusCode, body := postR6JSON(t, ctx, httpServer.Client(), httpServer.URL+"/admin/v1/merchant/versions/"+strconv.FormatInt(version.id, 10)+"/review", reviewBody)
	if statusCode != http.StatusOK || body["review_status"] != "approved" {
		t.Fatalf("review failed: %d %#v", statusCode, body)
	}
	version = loadR6Version(t, ctx, app, session.tenantID, version.id)
	publishBody := map[string]any{
		"confirm": true, "tenant_id": session.tenant, "shop_id": session.shop,
		"document_id": prefix + "-doc", "version_hash": version.sourceHash,
		"permission_revision": gateway.currentPermissionRevision(), "expected_fencing_epoch": version.fencing,
		"reason": "R6 isolated publication acceptance",
	}
	statusCode, body = postR6JSON(t, ctx, httpServer.Client(), httpServer.URL+"/admin/v1/merchant/versions/"+strconv.FormatInt(version.id, 10)+"/publish", publishBody)
	if statusCode != http.StatusOK || body["status"] != "active" {
		t.Fatalf("publish failed: %d %#v", statusCode, body)
	}
	version = loadR6Version(t, ctx, app, session.tenantID, version.id)
	publishBody["expected_fencing_epoch"] = version.fencing
	if err = gateway.refreshAuthorization(ctx); err != nil {
		t.Fatalf("refresh authorization before publish replay: %v", err)
	}
	publishBody["permission_revision"] = gateway.currentPermissionRevision()
	statusCode, body = postR6JSON(t, ctx, httpServer.Client(), httpServer.URL+"/admin/v1/merchant/versions/"+strconv.FormatInt(version.id, 10)+"/publish", publishBody)
	if statusCode != http.StatusOK || body["idempotent"] != true {
		t.Fatalf("publish replay was not stable: %d %#v", statusCode, body)
	}

	// A second version supersedes the first; rollback restores the first and a
	// replay of that rollback returns the same active result.
	content2 := []byte("# Shipping\n\nOrders leave within one business day.\n")
	manifest2 := r6Manifest(prefix+"-source", prefix+"-doc", "Shipping", "shipping.md", content2)
	second := submitR6(t, ctx, ingestClient, requestContext, prefix+"-second", manifest2, "shipping.md", content2)
	_, version2 := waitR6Reviewable(t, ctx, app, session.tenantID, mustInt64(t, second.JobId), prefix+"-doc")
	if err = gateway.refreshAuthorization(ctx); err != nil {
		t.Fatalf("refresh authorization after second Python submission: %v", err)
	}
	reviewBody["expected_hash"] = version2.sourceHash
	reviewBody["expected_fencing_epoch"] = version2.fencing
	reviewBody["permission_revision"] = gateway.currentPermissionRevision()
	statusCode, body = postR6JSON(t, ctx, httpServer.Client(), httpServer.URL+"/admin/v1/merchant/versions/"+strconv.FormatInt(version2.id, 10)+"/review", reviewBody)
	if statusCode != http.StatusOK || body["review_status"] != "approved" {
		t.Fatalf("second review failed: %d %#v", statusCode, body)
	}
	version2 = loadR6Version(t, ctx, app, session.tenantID, version2.id)
	publishBody["version_hash"] = version2.sourceHash
	publishBody["expected_fencing_epoch"] = version2.fencing
	publishBody["permission_revision"] = gateway.currentPermissionRevision()
	statusCode, body = postR6JSON(t, ctx, httpServer.Client(), httpServer.URL+"/admin/v1/merchant/versions/"+strconv.FormatInt(version2.id, 10)+"/publish", publishBody)
	if statusCode != http.StatusOK || body["status"] != "active" {
		t.Fatalf("second publish failed: %d %#v", statusCode, body)
	}
	if err = gateway.refreshAuthorization(ctx); err != nil {
		t.Fatalf("refresh authorization before rollback: %v", err)
	}
	version = loadR6Version(t, ctx, app, session.tenantID, version.id)
	if version.status != "superseded" {
		t.Fatalf("first version was not superseded: %+v", version)
	}
	rollbackBody := map[string]any{
		"confirm": true, "tenant_id": session.tenant, "shop_id": session.shop,
		"document_id": prefix + "-doc", "version_hash": version.sourceHash,
		"permission_revision": gateway.currentPermissionRevision(), "expected_fencing_epoch": version.fencing,
		"reason": "R6 isolated rollback acceptance",
	}
	statusCode, body = postR6JSON(t, ctx, httpServer.Client(), httpServer.URL+"/admin/v1/merchant/versions/"+strconv.FormatInt(version.id, 10)+"/rollback", rollbackBody)
	if statusCode != http.StatusOK || body["status"] != "active" {
		t.Fatalf("rollback failed: %d %#v", statusCode, body)
	}
	version = loadR6Version(t, ctx, app, session.tenantID, version.id)
	rollbackBody["expected_fencing_epoch"] = version.fencing
	if err = gateway.refreshAuthorization(ctx); err != nil {
		t.Fatalf("refresh authorization before rollback replay: %v", err)
	}
	rollbackBody["permission_revision"] = gateway.currentPermissionRevision()
	statusCode, body = postR6JSON(t, ctx, httpServer.Client(), httpServer.URL+"/admin/v1/merchant/versions/"+strconv.FormatInt(version.id, 10)+"/rollback", rollbackBody)
	if statusCode != http.StatusOK || body["idempotent"] != true {
		t.Fatalf("rollback replay was not stable: %d %#v", statusCode, body)
	}

	// Submit and cancel while the worker is idle.  The cancellation gate must
	// leave no active publication for the job.
	waitWorkerIdle(t, ctx, app)
	cancelContent := []byte("# Cancellation\n\nThis package will be cancelled.\n")
	cancelManifest := r6Manifest(prefix+"-cancel-source", prefix+"-cancel-doc", "Cancellation", "cancel.md", cancelContent)
	cancelled := submitR6(t, ctx, ingestClient, requestContext, prefix+"-cancel", cancelManifest, "cancel.md", cancelContent)
	_, err = ingestClient.Cancel(ctx, &ragv1.CancelRequest{Context: requestContext, JobId: cancelled.JobId})
	if err != nil {
		t.Fatalf("cancel request failed: %v", err)
	}
	if state := waitR6JobTerminal(t, ctx, app, session.tenantID, mustInt64(t, cancelled.JobId)); state != "cancelled" {
		t.Fatalf("cancelled job ended in %s", state)
	}
	var published int
	if err = app.QueryRow(ctx, `SELECT count(*) FROM ingest_publication p JOIN ingest_job_item i ON i.tenant_id=p.tenant_id AND i.id=p.item_id WHERE i.tenant_id=$1 AND i.job_id=$2`, session.tenantID, mustInt64(t, cancelled.JobId)).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if published != 0 {
		t.Fatalf("cancelled job was published: %d publication rows", published)
	}

	// An invalid package is failed atomically.  The worker must not leave a
	// document version or chunk behind for the failed logical key.
	empty := []byte{}
	badManifest := r6Manifest(prefix+"-bad-source", prefix+"-bad-doc", "Bad", "bad.md", empty)
	bad := submitR6(t, ctx, ingestClient, requestContext, prefix+"-bad", badManifest, "bad.md", empty)
	if state := waitR6JobTerminal(t, ctx, app, session.tenantID, mustInt64(t, bad.JobId)); state != "failed" {
		t.Fatalf("invalid package ended in %s", state)
	}
	var versions, chunks int
	if err = app.QueryRow(ctx, `SELECT count(*) FROM document_version v JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id WHERE v.tenant_id=$1 AND d.logical_key=$2`, session.tenantID, prefix+"-bad-doc").Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err = app.QueryRow(ctx, `SELECT count(*) FROM chunk c JOIN document d ON d.tenant_id=c.tenant_id AND d.id=c.doc_id WHERE c.tenant_id=$1 AND d.logical_key=$2`, session.tenantID, prefix+"-bad-doc").Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if versions != 0 || chunks != 0 {
		t.Fatalf("failed parse left durable rows: versions=%d chunks=%d", versions, chunks)
	}
}

type r6JobRow struct{ status string }

type r6VersionRow struct {
	id           int64
	status       string
	reviewStatus string
	sourceHash   string
	fencing      int64
	chunkCount   int
}

func freeLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func waitPythonReady(t *testing.T, ctx context.Context, client ragv1.RagServiceClient, scope *ragv1.RequestContext) {
	t.Helper()
	for {
		_, err := client.Understand(ctx, &ragv1.UnderstandRequest{Context: scope, Query: "R6 readiness"}, grpc.WaitForReady(true))
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("Python v2 process did not become ready: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func r6Manifest(sourceID, documentID, title, path string, content []byte) string {
	digest := sha256.Sum256(content)
	return fmt.Sprintf("schema_version: 1\nsource:\n  id: %s\n  name: R6 acceptance\npipeline_version: r6-fixture-v1\ndocuments:\n  - document_id: %s\n    title: %s\n    path: %s\n    format: markdown\n    sha256: %s\n    disclosure_class: external_allowed\n", sourceID, documentID, title, path, hex.EncodeToString(digest[:]))
}

func submitR6(t *testing.T, ctx context.Context, client ragv1.IngestServiceClient, scope *ragv1.RequestContext, key, manifest, path string, content []byte) *ragv1.SubmitResponse {
	t.Helper()
	response, err := client.Submit(ctx, &ragv1.SubmitRequest{
		Context: scope, IdempotencyKey: key, ManifestYaml: manifest,
		Files: []*ragv1.PackageFile{{Path: path, Content: content}}, ShopId: scope.ShopId,
		TenantDbId: strconv.FormatInt(scope.TenantDbId, 10), UserDbId: strconv.FormatInt(scope.UserDbId, 10),
	})
	if err != nil {
		t.Fatalf("submit %s: %v", key, err)
	}
	return response
}

func waitR6Reviewable(t *testing.T, ctx context.Context, db *pgxpool.Pool, tenantID, jobID int64, documentID string) (r6JobRow, r6VersionRow) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var job r6JobRow
		err := db.QueryRow(ctx, "SELECT status FROM ingest_job WHERE tenant_id=$1 AND id=$2", tenantID, jobID).Scan(&job.status)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		if err == nil && job.status == "awaiting_review" {
			var version r6VersionRow
			versionErr := db.QueryRow(ctx, `SELECT v.id,v.status,v.review_status,v.source_hash,v.fencing_epoch,COALESCE(v.chunk_count,0)
				FROM ingest_job_item i JOIN document d ON d.tenant_id=i.tenant_id AND d.logical_key=i.data_id
				JOIN document_version v ON v.tenant_id=i.tenant_id AND v.id=i.document_version_id
				WHERE i.tenant_id=$1 AND i.job_id=$2 AND d.logical_key=$3`, tenantID, jobID, documentID).
				Scan(&version.id, &version.status, &version.reviewStatus, &version.sourceHash, &version.fencing, &version.chunkCount)
			if versionErr == nil {
				return job, version
			}
			if !errors.Is(versionErr, pgx.ErrNoRows) {
				t.Fatal(versionErr)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("job %d did not become reviewable", jobID)
	return r6JobRow{}, r6VersionRow{}
}

func loadR6Version(t *testing.T, ctx context.Context, db *pgxpool.Pool, tenantID, versionID int64) r6VersionRow {
	t.Helper()
	var row r6VersionRow
	if err := db.QueryRow(ctx, `SELECT id,status,review_status,source_hash,fencing_epoch,COALESCE(chunk_count,0) FROM document_version WHERE tenant_id=$1 AND id=$2`, tenantID, versionID).
		Scan(&row.id, &row.status, &row.reviewStatus, &row.sourceHash, &row.fencing, &row.chunkCount); err != nil {
		t.Fatal(err)
	}
	return row
}

func assertR6ReviewBeforeActive(t *testing.T, ctx context.Context, db *pgxpool.Pool, tenantID, versionID int64) {
	t.Helper()
	row := loadR6Version(t, ctx, db, tenantID, versionID)
	if row.status == "active" || row.reviewStatus != "pending" {
		t.Fatalf("version became active before review: %+v", row)
	}
}

func waitR6JobTerminal(t *testing.T, ctx context.Context, db *pgxpool.Pool, tenantID, jobID int64) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var statusText string
		if err := db.QueryRow(ctx, "SELECT status FROM ingest_job WHERE tenant_id=$1 AND id=$2", tenantID, jobID).Scan(&statusText); err != nil {
			t.Fatal(err)
		}
		if statusText == "cancelled" || statusText == "failed" || statusText == "done" {
			return statusText
		}
		time.Sleep(100 * time.Millisecond)
	}
	var statusText, stage, errorCode string
	var cancelRequested bool
	if err := db.QueryRow(ctx, "SELECT status,stage,cancel_requested,COALESCE(error_code,'') FROM ingest_job WHERE tenant_id=$1 AND id=$2", tenantID, jobID).Scan(&statusText, &stage, &cancelRequested, &errorCode); err == nil {
		t.Fatalf("job %d did not reach a terminal state: status=%s stage=%s cancel_requested=%t error_code=%s", jobID, statusText, stage, cancelRequested, errorCode)
	}
	t.Fatalf("job %d did not reach a terminal state", jobID)
	return ""
}

func waitWorkerIdle(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var statusText string
		if err := db.QueryRow(ctx, "SELECT status FROM ingest_worker_heartbeat ORDER BY heartbeat_at DESC LIMIT 1").Scan(&statusText); err == nil && statusText == "idle" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Python ingestion worker did not report idle")
}

func postR6JSON(t *testing.T, ctx context.Context, client *http.Client, url string, body any) (int, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	if err = json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, decoded
}

func mustInt64(t *testing.T, value string) int64 {
	t.Helper()
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func cleanupR6Rows(ctx context.Context, db *pgxpool.Pool, tenantID int64, prefix string) {
	// Audits are deliberately retained.  These statements remove only the
	// temporary business rows created by this acceptance run.
	_, _ = db.Exec(ctx, `DELETE FROM ingest_publication p USING ingest_job_item i, ingest_job j WHERE p.tenant_id=$1 AND p.tenant_id=i.tenant_id AND p.item_id=i.id AND i.tenant_id=j.tenant_id AND i.job_id=j.id AND j.idempotency_key LIKE $2`, tenantID, prefix+"-%")
	_, _ = db.Exec(ctx, `DELETE FROM chunk c USING document d WHERE c.tenant_id=$1 AND c.doc_id=d.id AND d.tenant_id=$1 AND d.logical_key LIKE $2`, tenantID, prefix+"-%")
	_, _ = db.Exec(ctx, `DELETE FROM ingest_payload p USING ingest_job j WHERE p.tenant_id=$1 AND p.job_id=j.id AND j.idempotency_key LIKE $2`, tenantID, prefix+"-%")
	_, _ = db.Exec(ctx, `DELETE FROM ingest_job_item i USING ingest_job j WHERE i.tenant_id=$1 AND i.job_id=j.id AND j.idempotency_key LIKE $2`, tenantID, prefix+"-%")
	_, _ = db.Exec(ctx, `DELETE FROM ingest_job WHERE tenant_id=$1 AND idempotency_key LIKE $2`, tenantID, prefix+"-%")
	_, _ = db.Exec(ctx, `DELETE FROM document_version v USING document d WHERE v.tenant_id=$1 AND v.document_id=d.id AND d.tenant_id=$1 AND d.logical_key LIKE $2`, tenantID, prefix+"-%")
	_, _ = db.Exec(ctx, `DELETE FROM source_file sf USING source_manifest sm, source s WHERE sf.tenant_id=$1 AND sf.source_manifest_id=sm.id AND sm.tenant_id=s.tenant_id AND sm.source_id=s.id AND s.tenant_id=$1 AND s.uri LIKE $2`, tenantID, "admin://"+prefix+"-%")
	_, _ = db.Exec(ctx, `DELETE FROM source_manifest sm USING source s WHERE sm.tenant_id=$1 AND sm.source_id=s.id AND s.tenant_id=$1 AND s.uri LIKE $2`, tenantID, "admin://"+prefix+"-%")
	_, _ = db.Exec(ctx, `DELETE FROM document WHERE tenant_id=$1 AND logical_key LIKE $2`, tenantID, prefix+"-%")
	_, _ = db.Exec(ctx, `DELETE FROM source WHERE tenant_id=$1 AND uri LIKE $2`, tenantID, "admin://"+prefix+"-%")
}
