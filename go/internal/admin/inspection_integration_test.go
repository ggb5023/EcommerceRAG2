package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

type inspectionDBFixture struct {
	t         *testing.T
	ctx       context.Context
	api       http.Handler
	migration *pgxpool.Pool
	gateway   *pgxpool.Pool
	handler   *Handler
	alias     string
	tenant    int64
	user      int64
	shop      string
	source    int64
	stamp     int64
	turnNo    int
	customer  string
	convo     string
}

func newInspectionDBFixture(t *testing.T) *inspectionDBFixture {
	t.Helper()
	api, migration, gateway, alias, handler := integrationHandlerWithIngest(t, false)
	f := &inspectionDBFixture{
		t: t, ctx: context.Background(), api: api, migration: migration, gateway: gateway,
		handler: handler, alias: alias, tenant: handler.Session.TenantID,
		user: handler.Session.UserID, shop: handler.Session.Shop,
		stamp: time.Now().UnixNano(),
	}
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO source(tenant_id,shop_id,type)
		VALUES($1,$2,'inspection-test') RETURNING id`, f.tenant, f.shop).Scan(&f.source); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *inspectionDBFixture) createVersion(key, title, status string, effectiveTo *string, createdAt time.Time) (int64, int64) {
	f.t.Helper()
	var documentID, versionID int64
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO document(tenant_id,source_id,logical_key,title,doc_type,status)
		VALUES($1,$2,$3,$4,'markdown','active') RETURNING id`,
		f.tenant, f.source, key, title).Scan(&documentID); err != nil {
		f.t.Fatal(err)
	}
	hash := fmt.Sprintf("%064x", f.stamp+documentID)
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO document_version(tenant_id,document_id,source_hash,
		parser_version,chunk_rule_version,embedding_model,pipeline_version,object_key,status,effective_to,created_at)
		VALUES($1,$2,$3,'p','v1','e','p','object',$4,$5,$6) RETURNING id`,
		f.tenant, documentID, hash, status, effectiveTo, createdAt).Scan(&versionID); err != nil {
		f.t.Fatal(err)
	}
	if status == "active" {
		if _, err := f.migration.Exec(f.ctx, `UPDATE document SET active_version_id=$2
			WHERE tenant_id=$1 AND id=$3`, f.tenant, versionID, documentID); err != nil {
			f.t.Fatal(err)
		}
	}
	return documentID, versionID
}

func (f *inspectionDBFixture) addVersion(documentID int64, status string, createdAt time.Time) int64 {
	f.t.Helper()
	var versionID int64
	hash := fmt.Sprintf("%064x", f.stamp+documentID+1000)
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO document_version(tenant_id,document_id,source_hash,
		parser_version,chunk_rule_version,embedding_model,pipeline_version,object_key,status,created_at)
		VALUES($1,$2,$3,'p','v1','e','p','object',$4,$5) RETURNING id`,
		f.tenant, documentID, hash, status, createdAt).Scan(&versionID); err != nil {
		f.t.Fatal(err)
	}
	return versionID
}

func (f *inspectionDBFixture) createVisibleVersion(key, title, content string) (int64, int64, int64) {
	f.t.Helper()
	var documentID, versionID int64
	hash := fmt.Sprintf("%064x", f.stamp+5000)
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO document(tenant_id,source_id,logical_key,title,doc_type,status)
		VALUES($1,$2,$3,$4,'markdown','active') RETURNING id`,
		f.tenant, f.source, key, title).Scan(&documentID); err != nil {
		f.t.Fatal(err)
	}
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO document_version(tenant_id,document_id,source_hash,
		parser_version,chunk_rule_version,embedding_model,pipeline_version,object_key,status,
		review_status,reviewed_hash,reviewed_by,reviewed_at,disclosure_class,external_allowed)
		VALUES($1,$2,$3,'p','v1','e','p','object','active','approved',$3,$4,now(),'external_allowed',true)
		RETURNING id`, f.tenant, documentID, hash, f.user).Scan(&versionID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.migration.Exec(f.ctx, `UPDATE document SET active_version_id=$2
		WHERE tenant_id=$1 AND id=$3`, f.tenant, versionID, documentID); err != nil {
		f.t.Fatal(err)
	}
	var chunkID int64
	chunkHash := fmt.Sprintf("%064x", f.stamp+versionID)
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO chunk(version_id,doc_id,tenant_id,shop_id,chunk_index,
		section_seq,section_chunk_index,content,embed_text,token_count,content_type,split_reason,
		embedding_model,embedding_dim,tokenizer_id,chunk_hash)
		VALUES($1,$2,$3,$4,0,1,0,$5,$5,1,'paragraph','rule','test',1024,'test',$6) RETURNING id`,
		versionID, documentID, f.tenant, f.shop, content, chunkHash).Scan(&chunkID); err != nil {
		f.t.Fatal(err)
	}
	return documentID, versionID, chunkID
}

func (f *inspectionDBFixture) createEmployeeCitation(requestID, documentKey string, versionID int64, evidenceID, content string, at time.Time) {
	f.t.Helper()
	f.turnNo++
	var conversationID, turnID, executionID int64
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO conversation(tenant_id,user_id,shop_id,title)
		VALUES($1,$2,$3,'inspection') RETURNING id`, f.tenant, f.user, f.shop).Scan(&conversationID); err != nil {
		f.t.Fatal(err)
	}
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status,created_at)
		VALUES($1,$2,$3,$4,'inspection question','DONE',$5) RETURNING id`,
		f.tenant, conversationID, f.turnNo, requestID, at).Scan(&turnID); err != nil {
		f.t.Fatal(err)
	}
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,created_at,finished_at)
		VALUES($1,$2,1,$3,'DONE',false,$4,$4) RETURNING id`,
		f.tenant, turnID, requestID, at).Scan(&executionID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.migration.Exec(f.ctx, `INSERT INTO customer_reply(tenant_id,execution_id,text_plain,is_mock,can_copy,blocked_reason)
		VALUES($1,$2,'员工回答正文',false,true,'')`, f.tenant, executionID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.migration.Exec(f.ctx, `INSERT INTO evidence_snapshot(tenant_id,execution_id,evidence_id,document_id,
		version_id,source_ref,content,disclosure_class,customer_eligible,citation_index,shop_id)
		VALUES($1,$2,$3,$4,$5,'ref',$6,'external_allowed',true,1,$7)`,
		f.tenant, executionID, evidenceID, documentKey, fmt.Sprintf("%d", versionID), content, f.shop); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.migration.Exec(f.ctx, `INSERT INTO citation(tenant_id,execution_id,evidence_id,citation_index)
		VALUES($1,$2,$3,1)`, f.tenant, executionID, evidenceID); err != nil {
		f.t.Fatal(err)
	}
}

func (f *inspectionDBFixture) createCustomerMessage(messageID string, citations []*ragv1.Evidence, content string, at time.Time) {
	f.t.Helper()
	if f.customer == "" {
		f.customer = fmt.Sprintf("cust-%d", f.stamp)
		f.convo = fmt.Sprintf("custconv-%d", f.stamp)
		if _, err := f.migration.Exec(f.ctx, `INSERT INTO customer_identity(id,tenant_id,shop_id)
			VALUES($1,$2,$3)`, f.customer, f.tenant, f.shop); err != nil {
			f.t.Fatal(err)
		}
		if _, err := f.migration.Exec(f.ctx, `INSERT INTO customer_conversation(id,tenant_id,shop_id,customer_id)
			VALUES($1,$2,$3,$4)`, f.convo, f.tenant, f.shop, f.customer); err != nil {
			f.t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(citations)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.migration.Exec(f.ctx, `INSERT INTO customer_message(id,tenant_id,conversation_id,source,content,citations_json,created_at)
		VALUES($1,$2,$3,'assistant',$4,$5,$6)`,
		messageID, f.tenant, f.convo, content, encoded, at); err != nil {
		f.t.Fatal(err)
	}
}

func (f *inspectionDBFixture) createFailedTurn(query string, at time.Time) {
	f.t.Helper()
	f.turnNo++
	requestID := fmt.Sprintf("inspection-turn-%d-%d", f.stamp, f.turnNo)
	var conversationID, turnID, executionID int64
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO conversation(tenant_id,user_id,shop_id,title)
		VALUES($1,$2,$3,'inspection-gap') RETURNING id`, f.tenant, f.user, f.shop).Scan(&conversationID); err != nil {
		f.t.Fatal(err)
	}
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO turn(tenant_id,conversation_id,turn_no,request_id,user_query,status,created_at)
		VALUES($1,$2,$3,$4,$5,'FAILED',$6) RETURNING id`,
		f.tenant, conversationID, f.turnNo, requestID, query, at).Scan(&turnID); err != nil {
		f.t.Fatal(err)
	}
	if err := f.migration.QueryRow(f.ctx, `INSERT INTO conversation_execution(tenant_id,turn_id,execution_no,request_id,status,is_mock,created_at,finished_at)
		VALUES($1,$2,1,$3,'FAILED',true,$4,$4) RETURNING id`,
		f.tenant, turnID, requestID, at).Scan(&executionID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.migration.Exec(f.ctx, `INSERT INTO turn_decision(tenant_id,conversation_id,turn_id,state,execution_id)
		VALUES($1,$2::text,$3::text,'FAILED',$4)`,
		f.tenant, fmt.Sprintf("%d", conversationID), fmt.Sprintf("%d", turnID), executionID); err != nil {
		f.t.Fatal(err)
	}
}

func (f *inspectionDBFixture) visibleEvidence(chunkID int64, documentKey string, versionID int64, content string) *ragv1.Evidence {
	return &ragv1.Evidence{Id: fmt.Sprintf("db-chunk-%d", chunkID), DocumentId: documentKey,
		VersionId: fmt.Sprintf("%d", versionID), ShopId: f.shop, Content: content,
		DisclosureClass: "external_allowed", CustomerEligible: true, TenantId: f.alias}
}

func TestInspectionExpiredRuleBoundary(t *testing.T) {
	f := newInspectionDBFixture(t)
	businessDate, err := auth.ShopBusinessDate(f.ctx, f.gateway, f.tenant, f.shop)
	if err != nil {
		t.Fatal(err)
	}
	day, err := time.Parse("2006-01-02", businessDate)
	if err != nil {
		t.Fatal(err)
	}
	yesterday := day.AddDate(0, 0, -1).Format("2006-01-02")
	rows, err := adminStore.ExpiredVersions(f.ctx, f.gateway, f.tenant, f.shop, businessDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty fixture produced %d findings", len(rows))
	}
	f.createVersion("expired-one", "过期资料一", "active", &yesterday, time.Now())
	rows, err = adminStore.ExpiredVersions(f.ctx, f.gateway, f.tenant, f.shop, businessDate)
	if err != nil || len(rows) != 1 {
		t.Fatalf("one expired version: rows=%d err=%v", len(rows), err)
	}
	f.createVersion("expired-two", "过期资料二", "active", &yesterday, time.Now())
	rows, err = adminStore.ExpiredVersions(f.ctx, f.gateway, f.tenant, f.shop, businessDate)
	if err != nil || len(rows) != 2 {
		t.Fatalf("two expired versions: rows=%d err=%v", len(rows), err)
	}
	// effective_to == business_date is still valid and must not be reported.
	f.createVersion("expires-today", "今日到期", "active", &businessDate, time.Now())
	rows, err = adminStore.ExpiredVersions(f.ctx, f.gateway, f.tenant, f.shop, businessDate)
	if err != nil || len(rows) != 2 {
		t.Fatalf("same-day expiry: rows=%d err=%v", len(rows), err)
	}
	findings := expiredVersionFindings(f.tenant, f.shop, businessDate, rows)
	for _, finding := range findings {
		if finding.Kind != inspectionKindExpired || finding.Severity != inspectionSeverityHigh {
			t.Fatalf("unexpected finding: %+v", finding)
		}
	}
}

func TestInspectionNewerVersionConflictWindow(t *testing.T) {
	f := newInspectionDBFixture(t)
	from := time.Now().Add(-time.Duration(INSPECTION_WINDOW_DAYS) * 24 * time.Hour).Truncate(time.Microsecond)
	documentID, activeVersion := f.createVersion("conflict-doc", "冲突资料", "active", nil, from.Add(-2*time.Hour))
	f.addVersion(documentID, "pending", from.Add(-time.Hour))
	f.createEmployeeCitation("conflict-inside", "conflict-doc", activeVersion, "db-chunk-1", "content", from)
	rows, err := adminStore.NewerVersionConflicts(f.ctx, f.gateway, f.tenant, f.shop, from)
	if err != nil || len(rows) != 1 {
		t.Fatalf("inside window: rows=%d err=%v", len(rows), err)
	}
	if rows[0].DocumentID != documentID || len(rows[0].NewVersionIDs) != 1 || rows[0].EmployeeRefs != 1 {
		t.Fatalf("unexpected conflict row: %+v", rows[0])
	}
	documentTwo, activeTwo := f.createVersion("conflict-outside", "冲突资料二", "active", nil, from.Add(-3*time.Hour))
	f.addVersion(documentTwo, "pending", from.Add(-2*time.Hour))
	f.createEmployeeCitation("conflict-outside", "conflict-outside", activeTwo, "db-chunk-2", "content", from.Add(-time.Microsecond))
	rows, err = adminStore.NewerVersionConflicts(f.ctx, f.gateway, f.tenant, f.shop, from)
	if err != nil || len(rows) != 1 {
		t.Fatalf("outside window must not add a row: rows=%d err=%v", len(rows), err)
	}
}

func TestInspectionGapThresholdAndWindow(t *testing.T) {
	f := newInspectionDBFixture(t)
	from := time.Now().Add(-time.Duration(INSPECTION_WINDOW_DAYS) * 24 * time.Hour).Truncate(time.Microsecond)
	// Two turns just outside the window must not become a gap.
	f.createFailedTurn("退款要多久", from.Add(-time.Second))
	f.createFailedTurn("退款要多久", from.Add(-2*time.Second))
	gaps, err := adminStore.InspectionKnowledgeGaps(f.ctx, f.gateway, f.tenant, f.shop, from, INSPECTION_MIN_CLUSTER_TURNS)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Fatalf("window boundary leaked old turns: %+v", gaps)
	}
	// One in-window occurrence is below the cluster floor.
	f.createFailedTurn("发货要多久", from)
	gaps, err = adminStore.InspectionKnowledgeGaps(f.ctx, f.gateway, f.tenant, f.shop, from, INSPECTION_MIN_CLUSTER_TURNS)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Fatalf("cluster floor did not hold: %+v", gaps)
	}
	f.createFailedTurn("发货要多久！", from.Add(time.Second))
	gaps, err = adminStore.InspectionKnowledgeGaps(f.ctx, f.gateway, f.tenant, f.shop, from, INSPECTION_MIN_CLUSTER_TURNS)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].Count != 2 || gaps[0].Normalized != "发货要多久" {
		t.Fatalf("unexpected gaps: %+v", gaps)
	}
	findings := gapFindings(f.tenant, f.shop, gaps)
	if len(findings) != 1 || findings[0].Kind != inspectionKindUnanswered {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestInspectionCitationInvalid(t *testing.T) {
	f := newInspectionDBFixture(t)
	documentID, versionID, chunkID := f.createVisibleVersion("visible-doc", "可见资料", "可见内容")
	_ = documentID
	valid := f.visibleEvidence(chunkID, "visible-doc", versionID, "可见内容")
	invalid := f.visibleEvidence(chunkID+999999, "visible-doc", versionID, "可见内容")
	f.createCustomerMessage("msg-with-citation", []*ragv1.Evidence{valid, invalid}, "顾客对话正文", time.Now())
	f.createEmployeeCitation("req-valid-citation", "visible-doc", versionID, valid.Id, valid.Content, time.Now())
	f.createEmployeeCitation("req-invalid-citation", "visible-doc", versionID, invalid.Id, invalid.Content, time.Now())
	findings, err := f.handler.citationInspectionFindings(f.ctx, f.tenant, f.alias, f.shop)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings=%d want 2: %+v", len(findings), findings)
	}
	for _, finding := range findings {
		if finding.Kind != inspectionKindCitation || finding.Severity != inspectionSeverityHigh {
			t.Fatalf("unexpected finding: %+v", finding)
		}
		invalidCitations, ok := finding.Evidence["invalid_citations"].([]map[string]any)
		if !ok || len(invalidCitations) != 1 {
			t.Fatalf("invalid citations missing: %+v", finding.Evidence)
		}
	}
	raw, err := json.Marshal(findings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "顾客对话正文") || strings.Contains(string(raw), "员工回答正文") {
		t.Fatal("inspection evidence copied answer or customer conversation text")
	}
}

func TestInspectionRunIdempotentAndReopen(t *testing.T) {
	f := newInspectionDBFixture(t)
	businessDate, err := auth.ShopBusinessDate(f.ctx, f.gateway, f.tenant, f.shop)
	if err != nil {
		t.Fatal(err)
	}
	day, err := time.Parse("2006-01-02", businessDate)
	if err != nil {
		t.Fatal(err)
	}
	yesterday := day.AddDate(0, 0, -1).Format("2006-01-02")
	f.createVersion("reopen-doc", "重开资料", "active", &yesterday, time.Now())

	first := apiJSON(t, f.api, "POST", "/admin/v1/inspections/run", nil, http.StatusOK)
	if inserted, updated, _ := inspectionKindCounts(t, first, inspectionKindExpired); inserted != 1 || updated != 0 {
		t.Fatalf("first run inserted=%d updated=%d", inserted, updated)
	}
	second := apiJSON(t, f.api, "POST", "/admin/v1/inspections/run", nil, http.StatusOK)
	if inserted, updated, _ := inspectionKindCounts(t, second, inspectionKindExpired); inserted != 0 || updated != 1 {
		t.Fatalf("second run inserted=%d updated=%d", inserted, updated)
	}
	var openCount int
	if err := f.migration.QueryRow(f.ctx, `SELECT count(*) FROM inspection_report
		WHERE tenant_id=$1 AND kind=$2 AND status='open'`, f.tenant, inspectionKindExpired).Scan(&openCount); err != nil {
		t.Fatal(err)
	}
	if openCount != 1 {
		t.Fatalf("open reports=%d want 1", openCount)
	}
	list := apiJSON(t, f.api, "GET", "/admin/v1/inspections?status=open", nil, http.StatusOK)
	items, ok := list["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("list did not return items: %+v", list)
	}
	var reportID float64
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || item["kind"] != inspectionKindExpired {
			continue
		}
		reportID, _ = item["id"].(float64)
	}
	if reportID == 0 {
		t.Fatalf("expired report missing from list: %+v", items)
	}
	path := fmt.Sprintf("/admin/v1/inspections/%d/status", int64(reportID))
	ack := apiJSON(t, f.api, "POST", path, map[string]any{"status": "acknowledged"}, http.StatusOK)
	if ack["status"] != "acknowledged" {
		t.Fatalf("acknowledged response: %+v", ack)
	}
	resolved := apiJSON(t, f.api, "POST", path, map[string]any{"status": "resolved"}, http.StatusOK)
	if resolved["status"] != "resolved" || resolved["resolved_at"] == nil {
		t.Fatalf("resolved response: %+v", resolved)
	}
	apiJSON(t, f.api, "POST", path, map[string]any{"status": "resolved"}, http.StatusConflict)

	third := apiJSON(t, f.api, "POST", "/admin/v1/inspections/run", nil, http.StatusOK)
	if inserted, _, _ := inspectionKindCounts(t, third, inspectionKindExpired); inserted != 1 {
		t.Fatalf("reopen inserted=%d want 1", inserted)
	}
	if err := f.migration.QueryRow(f.ctx, `SELECT count(*) FROM inspection_report
		WHERE tenant_id=$1 AND kind=$2 AND status='open'`, f.tenant, inspectionKindExpired).Scan(&openCount); err != nil {
		t.Fatal(err)
	}
	if openCount != 1 {
		t.Fatalf("reopened open reports=%d want 1", openCount)
	}
	var total int
	if err := f.migration.QueryRow(f.ctx, `SELECT count(*) FROM inspection_report
		WHERE tenant_id=$1 AND kind=$2`, f.tenant, inspectionKindExpired).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("closed+reopened rows=%d want 2", total)
	}
}

func inspectionKindCounts(t *testing.T, response map[string]any, kind string) (int, int, int) {
	t.Helper()
	byKind, ok := response["by_kind"].(map[string]any)
	if !ok {
		t.Fatalf("by_kind missing: %+v", response)
	}
	raw, ok := byKind[kind].(map[string]any)
	if !ok {
		t.Fatalf("%s summary missing: %+v", kind, byKind)
	}
	number := func(key string) int {
		value, _ := raw[key].(float64)
		return int(value)
	}
	return number("inserted"), number("updated"), number("closed")
}
