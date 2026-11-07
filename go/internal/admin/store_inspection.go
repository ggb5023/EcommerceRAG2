package admin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	errInspectionNotFound = errors.New("inspection report not found")
	errInspectionClosed   = errors.New("inspection report is already closed")
)

type inspectionShopRow struct {
	ID       string
	Timezone string
}

type expiredVersionRow struct {
	DocumentID  int64
	LogicalKey  string
	Title       string
	VersionID   string
	EffectiveTo string
}

type newerVersionConflictRow struct {
	DocumentID     int64
	LogicalKey     string
	Title          string
	VersionID      string
	VersionCreated time.Time
	NewVersionIDs  []string
	EmployeeRefs   int64
	CustomerRefs   int64
}

type inspectionGatewayCitation struct {
	RequestID        string
	EvidenceID       string
	DocumentID       string
	VersionID        string
	ShopID           string
	Content          string
	DisclosureClass  string
	CustomerEligible bool
}

type inspectionCustomerCitation struct {
	MessageID string
	Citations []byte
}

type inspectionReportRow struct {
	ID          int64
	TenantID    int64
	ShopID      string
	Kind        string
	Severity    string
	Status      string
	SubjectType string
	SubjectID   string
	Fingerprint string
	Evidence    json.RawMessage
	DetectedAt  time.Time
	ResolvedAt  *time.Time
}

// InspectionShops returns the shops in the caller's scope. A tenant-wide grant
// sees every shop; a shop grant sees only the explicitly allowed ids.
func (s *Store) InspectionShops(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string, tenantWide bool) ([]inspectionShopRow, error) {
	rows, err := db.Query(ctx, `SELECT id,COALESCE(timezone,'Asia/Shanghai') FROM shop
		WHERE tenant_id=$1 AND ($3::boolean OR id=ANY($2::text[])) ORDER BY id`, tenantID, shops, tenantWide)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]inspectionShopRow, 0)
	for rows.Next() {
		var row inspectionShopRow
		if err := rows.Scan(&row.ID, &row.Timezone); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// ExpiredVersions finds active versions whose effective window ended before the
// shop's business date. The caller supplies businessDate from auth.ShopBusinessDate,
// the single M7 definition of the merchant calendar day.
func (s *Store) ExpiredVersions(ctx context.Context, db *pgxpool.Pool, tenantID int64, shopID, businessDate string) ([]expiredVersionRow, error) {
	rows, err := db.Query(ctx, `SELECT d.id,d.logical_key,COALESCE(d.title,d.logical_key),
			v.id::text,v.effective_to::text
		FROM document_version v
		JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
		JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
		WHERE v.tenant_id=$1 AND s.shop_id=$2 AND v.status='active'
		  AND v.effective_to IS NOT NULL AND v.effective_to < $3::date
		ORDER BY v.id`, tenantID, shopID, businessDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]expiredVersionRow, 0)
	for rows.Next() {
		var row expiredVersionRow
		if err := rows.Scan(&row.DocumentID, &row.LogicalKey, &row.Title, &row.VersionID, &row.EffectiveTo); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// NewerVersionConflicts finds an active version that is still cited while the
// same document already has a newer active or pending version. The employee
// side reads the citation/evidence snapshot; the customer side reads the
// customer_message citation snapshot. Both are bounded by the same window start.
func (s *Store) NewerVersionConflicts(ctx context.Context, db *pgxpool.Pool, tenantID int64, shopID string, from time.Time) ([]newerVersionConflictRow, error) {
	rows, err := db.Query(ctx, `WITH candidates AS (
			SELECT d.id AS document_id,d.logical_key,COALESCE(d.title,d.logical_key) AS title,
				v.id AS version_id,v.created_at,
				COALESCE((SELECT array_agg(n.id::text ORDER BY n.created_at,n.id)
					FROM document_version n
					WHERE n.tenant_id=v.tenant_id AND n.document_id=v.document_id AND n.id<>v.id
					  AND n.status IN ('active','pending')
					  AND (n.created_at>v.created_at OR (n.created_at=v.created_at AND n.id>v.id))),'{}') AS new_versions,
				(SELECT count(DISTINCT es.execution_id) FROM citation c
					JOIN evidence_snapshot es ON es.tenant_id=c.tenant_id AND es.execution_id=c.execution_id AND es.evidence_id=c.evidence_id
					JOIN conversation_execution e ON e.tenant_id=c.tenant_id AND e.id=c.execution_id
					JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
					JOIN conversation conv ON conv.tenant_id=t.tenant_id AND conv.id=t.conversation_id
					WHERE es.tenant_id=v.tenant_id AND es.version_id=v.id::text AND conv.shop_id=$2
					  AND e.is_mock=false AND e.created_at>=$3) AS employee_refs,
				(SELECT count(DISTINCT m.id) FROM customer_message m
					JOIN customer_conversation cc ON cc.tenant_id=m.tenant_id AND cc.id=m.conversation_id
					LEFT JOIN customer_execution ce ON ce.tenant_id=m.tenant_id AND ce.id=m.execution_id
					WHERE m.tenant_id=v.tenant_id AND cc.shop_id=$2 AND m.source='assistant'
					  AND (m.execution_id IS NULL OR ce.is_mock=false) AND m.created_at>=$3
					  AND EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(m.citations_json)='array' THEN m.citations_json ELSE '[]'::jsonb END) ce2
						WHERE ce2->>'version_id'=v.id::text)) AS customer_refs
			FROM document_version v
			JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id
			JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id
			WHERE v.tenant_id=$1 AND s.shop_id=$2 AND v.status='active'
		)
		SELECT document_id,logical_key,title,version_id::text,created_at,new_versions,employee_refs,customer_refs
		FROM candidates
		WHERE cardinality(new_versions)>0 AND (employee_refs>0 OR customer_refs>0)
		ORDER BY document_id`, tenantID, shopID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]newerVersionConflictRow, 0)
	for rows.Next() {
		var row newerVersionConflictRow
		if err := rows.Scan(&row.DocumentID, &row.LogicalKey, &row.Title, &row.VersionID,
			&row.VersionCreated, &row.NewVersionIDs, &row.EmployeeRefs, &row.CustomerRefs); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// inspectionKnowledgeGapsSQL is the shop-scoped form of knowledgeGapsSQL. The
// clustering, normalization and thresholds stay in summarizeKnowledgeGaps; only
// the candidate turn read adds the conversation.shop_id predicate.
const inspectionKnowledgeGapsSQL = `WITH latest AS (
			SELECT t.id, t.conversation_id, t.parent_turn_id, t.created_at, t.user_query,
				decision.state, decision.finished_at
			FROM turn t
			JOIN conversation c ON c.tenant_id=t.tenant_id AND c.id=t.conversation_id
			LEFT JOIN LATERAL (
				SELECT td.state, e.finished_at
				FROM conversation_execution e
				LEFT JOIN turn_decision td ON td.tenant_id=e.tenant_id AND td.execution_id=e.id
				WHERE e.tenant_id=t.tenant_id AND e.turn_id=t.id
				ORDER BY e.execution_no DESC
				LIMIT 1
			) decision ON true
			WHERE t.tenant_id=$1 AND t.created_at>=$2 AND t.kind='user' AND c.shop_id=$3
		)
		SELECT id, conversation_id, parent_turn_id, created_at, user_query, state, finished_at
		FROM latest
		WHERE state IN ('FAILED','ASKING')
		   OR (state='DONE' AND id IN (
		       SELECT parent_turn_id FROM latest WHERE state='ASKING' AND parent_turn_id IS NOT NULL
		   ))
		ORDER BY created_at DESC
		LIMIT $4`

// InspectionKnowledgeGaps reuses the existing gap aggregation for one shop.
func (s *Store) InspectionKnowledgeGaps(ctx context.Context, db *pgxpool.Pool, tenantID int64, shopID string, from time.Time, minCount int64) ([]KnowledgeGap, error) {
	rows, err := db.Query(ctx, inspectionKnowledgeGapsSQL, tenantID, from, shopID, resolutionFetchLimit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	turns := make([]resolutionTurn, 0)
	for rows.Next() {
		var turn resolutionTurn
		if err := rows.Scan(&turn.TurnID, &turn.ConversationID, &turn.ParentTurnID, &turn.CreatedAt,
			&turn.UserQuery, &turn.State, &turn.FinishedAt); err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(turns) > resolutionFetchLimit {
		turns = turns[:resolutionFetchLimit]
	}
	return summarizeKnowledgeGaps(turns, minCount, time.Now()), nil
}

// InspectionGatewayCitations reads cited evidence of completed employee answers.
// Only citation rows count as "referenced"; an evidence row that was retrieved
// but never cited is not an answer citation.
func (s *Store) InspectionGatewayCitations(ctx context.Context, db *pgxpool.Pool, tenantID int64, shopID string) ([]inspectionGatewayCitation, error) {
	rows, err := db.Query(ctx, `SELECT e.request_id,es.evidence_id,COALESCE(es.document_id,''),
			COALESCE(es.version_id,''),COALESCE(NULLIF(es.shop_id,''),$2),es.content,
			es.disclosure_class,es.customer_eligible
		FROM citation c
		JOIN evidence_snapshot es ON es.tenant_id=c.tenant_id AND es.execution_id=c.execution_id AND es.evidence_id=c.evidence_id
		JOIN conversation_execution e ON e.tenant_id=c.tenant_id AND e.id=c.execution_id
		JOIN turn t ON t.tenant_id=e.tenant_id AND t.id=e.turn_id
		JOIN conversation conv ON conv.tenant_id=t.tenant_id AND conv.id=t.conversation_id
		JOIN customer_reply cr ON cr.tenant_id=e.tenant_id AND cr.execution_id=e.id
		WHERE c.tenant_id=$1 AND conv.shop_id=$2 AND e.status='DONE' AND e.is_mock=false
		ORDER BY e.id,c.citation_index`, tenantID, shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]inspectionGatewayCitation, 0)
	for rows.Next() {
		var row inspectionGatewayCitation
		if err := rows.Scan(&row.RequestID, &row.EvidenceID, &row.DocumentID, &row.VersionID,
			&row.ShopID, &row.Content, &row.DisclosureClass, &row.CustomerEligible); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// InspectionCustomerCitations reads cited evidence snapshots of customer answers.
func (s *Store) InspectionCustomerCitations(ctx context.Context, db *pgxpool.Pool, tenantID int64, shopID string) ([]inspectionCustomerCitation, error) {
	rows, err := db.Query(ctx, `SELECT m.id,m.citations_json
		FROM customer_message m
		JOIN customer_conversation cc ON cc.tenant_id=m.tenant_id AND cc.id=m.conversation_id
		LEFT JOIN customer_execution ce ON ce.tenant_id=m.tenant_id AND ce.id=m.execution_id
		WHERE m.tenant_id=$1 AND cc.shop_id=$2 AND m.source='assistant'
		  AND (m.execution_id IS NULL OR ce.is_mock=false)
		  AND CASE WHEN jsonb_typeof(m.citations_json)='array' THEN jsonb_array_length(m.citations_json) ELSE 0 END>0
		ORDER BY m.created_at DESC,m.id`, tenantID, shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]inspectionCustomerCitation, 0)
	for rows.Next() {
		var row inspectionCustomerCitation
		if err := rows.Scan(&row.MessageID, &row.Citations); err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// InspectionDocumentTitles resolves the logical keys stored in evidence
// snapshots to human titles for the evidence panel.
func (s *Store) InspectionDocumentTitles(ctx context.Context, db *pgxpool.Pool, tenantID int64, logicalKeys []string) (map[string]string, error) {
	titles := make(map[string]string, len(logicalKeys))
	if len(logicalKeys) == 0 {
		return titles, nil
	}
	rows, err := db.Query(ctx, `SELECT logical_key,COALESCE(title,logical_key)
		FROM document WHERE tenant_id=$1 AND logical_key=ANY($2::text[])`, tenantID, logicalKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, title string
		if err := rows.Scan(&key, &title); err != nil {
			return nil, err
		}
		titles[key] = title
	}
	return titles, rows.Err()
}

// UpsertInspectionReport keeps one open row per fingerprint and refreshes it on
// re-detection. A closed row does not conflict, so the next detection inserts a
// fresh open row and therefore reopens the finding.
func (s *Store) UpsertInspectionReport(ctx context.Context, tx pgx.Tx, row inspectionReportRow) (bool, error) {
	var inserted bool
	err := tx.QueryRow(ctx, `INSERT INTO inspection_report(tenant_id,shop_id,kind,severity,status,subject_type,subject_id,fingerprint,evidence_json,detected_at)
		VALUES($1,NULLIF($2,''),$3,$4,'open',$5,$6,$7,$8,now())
		ON CONFLICT (tenant_id,fingerprint) WHERE status='open'
		DO UPDATE SET evidence_json=EXCLUDED.evidence_json,detected_at=EXCLUDED.detected_at,
			severity=EXCLUDED.severity,shop_id=EXCLUDED.shop_id
		RETURNING (xmax=0)`, row.TenantID, row.ShopID, row.Kind, row.Severity,
		row.SubjectType, row.SubjectID, row.Fingerprint, row.Evidence).Scan(&inserted)
	return inserted, err
}

// CloseMissingInspectionReports resolves open rows of one kind that this run no
// longer detected. Only the scanned shops are touched.
func (s *Store) CloseMissingInspectionReports(ctx context.Context, tx pgx.Tx, tenantID int64, shops []string, tenantWide bool, kind string, keep []string) (int64, error) {
	tag, err := tx.Exec(ctx, `UPDATE inspection_report SET status='resolved',resolved_at=now()
		WHERE tenant_id=$1 AND status='open' AND kind=$2
		  AND ($3::boolean OR shop_id=ANY($4::text[]))
		  AND NOT (fingerprint=ANY(COALESCE($5::text[],'{}'::text[])))`,
		tenantID, kind, tenantWide, shops, keep)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

const inspectionReportColumns = `id,tenant_id,COALESCE(shop_id,''),kind,severity,status,
	subject_type,subject_id,fingerprint,evidence_json,detected_at,resolved_at`

func scanInspectionReport(scanner interface{ Scan(...any) error }) (inspectionReportRow, error) {
	var row inspectionReportRow
	err := scanner.Scan(&row.ID, &row.TenantID, &row.ShopID, &row.Kind, &row.Severity, &row.Status,
		&row.SubjectType, &row.SubjectID, &row.Fingerprint, &row.Evidence, &row.DetectedAt, &row.ResolvedAt)
	return row, err
}

// InspectionReports lists reports in one status, ordered by severity and then
// newest detection.
func (s *Store) InspectionReports(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string, tenantWide bool, status string) ([]inspectionReportRow, error) {
	rows, err := db.Query(ctx, `SELECT `+inspectionReportColumns+`
		FROM inspection_report
		WHERE tenant_id=$1 AND status=$2 AND ($3::boolean OR shop_id=ANY($4::text[]))
		ORDER BY CASE severity WHEN 'high' THEN 0 WHEN 'medium' THEN 1 ELSE 2 END,
			detected_at DESC,id DESC LIMIT 500`, tenantID, status, tenantWide, shops)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]inspectionReportRow, 0)
	for rows.Next() {
		row, err := scanInspectionReport(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, row)
	}
	return items, rows.Err()
}

// InspectionReport returns one report visible in the caller's shop scope.
func (s *Store) InspectionReport(ctx context.Context, db *pgxpool.Pool, tenantID, id int64, shops []string, tenantWide bool) (inspectionReportRow, error) {
	row, err := scanInspectionReport(db.QueryRow(ctx, `SELECT `+inspectionReportColumns+`
		FROM inspection_report WHERE tenant_id=$1 AND id=$2 AND ($3::boolean OR shop_id=ANY($4::text[]))`,
		tenantID, id, tenantWide, shops))
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errInspectionNotFound
	}
	return row, err
}

// UpdateInspectionStatus applies a merchant triage action. Open and
// acknowledged rows may move to acknowledged, resolved or dismissed; a closed
// row is terminal until a later inspection opens a new row.
func (s *Store) UpdateInspectionStatus(ctx context.Context, db *pgxpool.Pool, tenantID, id int64, shops []string, tenantWide bool, status string) (inspectionReportRow, error) {
	row, err := scanInspectionReport(db.QueryRow(ctx, `UPDATE inspection_report
		SET status=$5,resolved_at=CASE WHEN $5 IN ('resolved','dismissed') THEN now() ELSE NULL END
		WHERE tenant_id=$1 AND id=$2 AND ($3::boolean OR shop_id=ANY($4::text[]))
		  AND status IN ('open','acknowledged')
		RETURNING `+inspectionReportColumns, tenantID, id, tenantWide, shops, status))
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	var current string
	lookup := db.QueryRow(ctx, `SELECT status FROM inspection_report
		WHERE tenant_id=$1 AND id=$2 AND ($3::boolean OR shop_id=ANY($4::text[]))`,
		tenantID, id, tenantWide, shops)
	if err := lookup.Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return row, errInspectionNotFound
		}
		return row, err
	}
	return row, errInspectionClosed
}
