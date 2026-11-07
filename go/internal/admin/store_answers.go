package admin

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AnswerRow is one staff turn or customer assistant message in the review list.
// Customer identity, session identifiers and addresses are never selected.
type AnswerRow struct {
	Kind          string
	ID            string
	RequestID     string
	CreatedAt     time.Time
	ShopID        string
	Question      string
	Answer        string
	CitationCount int
	FactCount     int
	IsMock        bool
	Behavior      string
	State         string
}

// AnswerCitation is one cited evidence snapshot with its current validity.
type AnswerCitation struct {
	Index           int    `json:"citation_index"`
	EvidenceID      string `json:"evidence_id"`
	DocumentID      string `json:"document_id"`
	VersionID       string `json:"version_id"`
	Title           string `json:"title"`
	Snippet         string `json:"snippet"`
	DisclosureClass string `json:"disclosure_class"`
	Valid           bool   `json:"valid"`
}

// AnswerFact is one persisted read-only fact snapshot.
type AnswerFact struct {
	ToolName   string `json:"tool_name"`
	ResourceID string `json:"resource_id"`
	ObservedAt string `json:"observed_at"`
	ValueJSON  string `json:"value_json"`
	IsMock     bool   `json:"is_mock"`
}

type AnswerDetail struct {
	AnswerRow
	Citations []AnswerCitation `json:"citations"`
	Facts     []AnswerFact     `json:"facts"`
}

var errAnswerNotFound = errors.New("answer_not_found")

const staffAnswerRowsSQL = `SELECT t.id,COALESCE(e.request_id,''),t.created_at,conv.shop_id,t.user_query,
	COALESCE(e.answer,''),(SELECT count(*) FROM citation c WHERE c.tenant_id=e.tenant_id AND c.execution_id=e.id),
	CASE WHEN jsonb_typeof(e.facts_json)='array' THEN jsonb_array_length(e.facts_json) ELSE 0 END,e.is_mock,e.status,e.status
	FROM turn t
	JOIN conversation conv ON conv.tenant_id=t.tenant_id AND conv.id=t.conversation_id
	JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
	  AND e.execution_no=(SELECT max(x.execution_no) FROM conversation_execution x
	    WHERE x.tenant_id=t.tenant_id AND x.turn_id=t.id)
	WHERE t.tenant_id=$1 AND conv.shop_id=ANY($2::text[]) AND t.kind='user'
	  AND t.status IN ('DONE','ASKING') AND COALESCE(e.answer,'')<>''
	  AND ($3::bool IS NULL OR e.is_mock=$3)
	  AND ($4::timestamptz IS NULL OR (t.created_at, 'staff', t.id::text) < ($4::timestamptz, $5::text, $6::text))
	ORDER BY t.created_at DESC,t.id::text DESC LIMIT $7`

const customerAnswerRowsSQL = `SELECT m.id,COALESCE(e.id,''),m.created_at,cc.shop_id,
	COALESCE((SELECT cm.content FROM customer_message cm
	  WHERE cm.tenant_id=m.tenant_id AND cm.conversation_id=m.conversation_id
	    AND cm.source='customer' AND cm.created_at<=m.created_at
	  ORDER BY cm.created_at DESC,cm.id DESC LIMIT 1),''),
	m.content,CASE WHEN jsonb_typeof(m.citations_json)='array' THEN jsonb_array_length(m.citations_json) ELSE 0 END,CASE WHEN jsonb_typeof(e.facts_json)='array' THEN jsonb_array_length(e.facts_json) ELSE 0 END,
	COALESCE(e.is_mock,false),COALESCE(e.behavior,''),COALESCE(e.state,'')
	FROM customer_message m
	JOIN customer_conversation cc ON cc.tenant_id=m.tenant_id AND cc.id=m.conversation_id
	LEFT JOIN customer_execution e ON e.tenant_id=m.tenant_id AND e.id=m.execution_id
	WHERE m.tenant_id=$1 AND cc.shop_id=ANY($2::text[]) AND m.source='assistant'
	  AND ($3::bool IS NULL OR COALESCE(e.is_mock,false)=$3)
	  AND ($4::timestamptz IS NULL OR (m.created_at, 'customer', m.id) < ($4::timestamptz, $5::text, $6::text))
	ORDER BY m.created_at DESC,m.id::text DESC LIMIT $7`

// AnswerCursor is the composite page cursor. The timestamp alone skips rows
// that share the boundary microsecond across the two answer sources.
type AnswerCursor struct {
	CreatedAt time.Time
	Kind      string
	ID        string
}

func (c AnswerCursor) String() string {
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.Kind + "|" + c.ID
}

// MerchantAnswers merges both answer sources, newest first, and returns the
// cursor for the next page only when more rows exist.
func (s *Store) MerchantAnswers(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string,
	kind string, mock *bool, before *AnswerCursor, limit int) ([]AnswerRow, *AnswerCursor, error) {
	rows := []AnswerRow{}
	var beforeTime *time.Time
	beforeKind, beforeID := "", ""
	if before != nil {
		beforeTime, beforeKind, beforeID = &before.CreatedAt, before.Kind, before.ID
	}
	collect := func(query, answerKind string) error {
		result, err := db.Query(ctx, query, tenantID, shops, mock, beforeTime, beforeKind, beforeID, limit+1)
		if err != nil {
			return err
		}
		defer result.Close()
		for result.Next() {
			var row AnswerRow
			row.Kind = answerKind
			if err = result.Scan(&row.ID, &row.RequestID, &row.CreatedAt, &row.ShopID, &row.Question,
				&row.Answer, &row.CitationCount, &row.FactCount, &row.IsMock, &row.Behavior, &row.State); err != nil {
				return err
			}
			if answerKind == "staff" {
				row.Behavior = staffAnswerBehavior(row.State)
			} else {
				row.Behavior = normalizeAnswerBehavior(row.Behavior)
			}
			rows = append(rows, row)
		}
		return result.Err()
	}
	if kind != "customer" {
		if err := collect(staffAnswerRowsSQL, "staff"); err != nil {
			return nil, nil, err
		}
	}
	if kind != "staff" {
		if err := collect(customerAnswerRowsSQL, "customer"); err != nil {
			return nil, nil, err
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind > rows[j].Kind
		}
		return rows[i].ID > rows[j].ID
	})
	var next *AnswerCursor
	if len(rows) > limit {
		rows = rows[:limit]
		cursor := AnswerCursor{CreatedAt: rows[len(rows)-1].CreatedAt, Kind: rows[len(rows)-1].Kind, ID: rows[len(rows)-1].ID}
		next = &cursor
	}
	return rows, next, nil
}

const staffAnswerDetailSQL = `SELECT t.id,COALESCE(e.request_id,''),t.created_at,conv.shop_id,t.user_query,
	COALESCE(e.answer,''),(SELECT count(*) FROM citation c WHERE c.tenant_id=e.tenant_id AND c.execution_id=e.id),
	CASE WHEN jsonb_typeof(e.facts_json)='array' THEN jsonb_array_length(e.facts_json) ELSE 0 END,e.is_mock,e.status,e.status,
	e.id,CASE WHEN jsonb_typeof(e.facts_json)='array' THEN e.facts_json ELSE '[]'::jsonb END
	FROM turn t
	JOIN conversation conv ON conv.tenant_id=t.tenant_id AND conv.id=t.conversation_id
	JOIN conversation_execution e ON e.tenant_id=t.tenant_id AND e.turn_id=t.id
	WHERE t.tenant_id=$1 AND t.id=$2 AND conv.shop_id=ANY($3::text[]) AND t.kind='user'
	ORDER BY e.execution_no DESC LIMIT 1`

const customerAnswerDetailSQL = `SELECT m.id,COALESCE(e.id,''),m.created_at,cc.shop_id,
	COALESCE((SELECT cm.content FROM customer_message cm
	  WHERE cm.tenant_id=m.tenant_id AND cm.conversation_id=m.conversation_id
	    AND cm.source='customer' AND cm.created_at<=m.created_at
	  ORDER BY cm.created_at DESC,cm.id DESC LIMIT 1),''),
	m.content,CASE WHEN jsonb_typeof(m.citations_json)='array' THEN jsonb_array_length(m.citations_json) ELSE 0 END,CASE WHEN jsonb_typeof(e.facts_json)='array' THEN jsonb_array_length(e.facts_json) ELSE 0 END,
	COALESCE(e.is_mock,false),COALESCE(e.behavior,''),COALESCE(e.state,''),
	COALESCE(e.id,''),CASE WHEN jsonb_typeof(e.facts_json)='array' THEN e.facts_json ELSE '[]'::jsonb END,CASE WHEN jsonb_typeof(m.citations_json)='array' THEN m.citations_json ELSE '[]'::jsonb END
	FROM customer_message m
	JOIN customer_conversation cc ON cc.tenant_id=m.tenant_id AND cc.id=m.conversation_id
	LEFT JOIN customer_execution e ON e.tenant_id=m.tenant_id AND e.id=m.execution_id
	WHERE m.tenant_id=$1 AND m.id=$2 AND cc.shop_id=ANY($3::text[]) AND m.source='assistant'`

const staffAnswerCitationsSQL = `SELECT c.citation_index,es.evidence_id,COALESCE(es.document_id,''),
	COALESCE(es.version_id,''),COALESCE(d.title,''),COALESCE(es.content,''),COALESCE(es.disclosure_class,''),
	EXISTS(SELECT 1 FROM document active JOIN document_version v ON v.tenant_id=active.tenant_id AND v.id::text=es.version_id
	  WHERE active.tenant_id=c.tenant_id AND active.logical_key=es.document_id AND active.status='active'
	    AND active.active_version_id=v.id AND v.status='active' AND v.review_status='approved')
	FROM citation c
	JOIN evidence_snapshot es ON es.tenant_id=c.tenant_id AND es.execution_id=c.execution_id AND es.evidence_id=c.evidence_id
	LEFT JOIN document d ON d.tenant_id=c.tenant_id AND d.logical_key=es.document_id
	WHERE c.tenant_id=$1 AND c.execution_id=$2 ORDER BY c.citation_index`

const answerDocumentTitlesSQL = `SELECT d.logical_key,d.title,COALESCE(d.active_version_id::text,''),
	COALESCE(v.status,''),COALESCE(v.review_status,''),COALESCE(d.status,'')
	FROM document d LEFT JOIN document_version v ON v.tenant_id=d.tenant_id AND v.id=d.active_version_id
	WHERE d.tenant_id=$1 AND d.logical_key=ANY($2::text[])`

// MerchantAnswer returns the full answer, citations and facts for one record.
func (s *Store) MerchantAnswer(ctx context.Context, db *pgxpool.Pool, tenantID int64, shops []string,
	kind, id string) (AnswerDetail, error) {
	var detail AnswerDetail
	detail.Kind, detail.ID = kind, id
	var executionID, factsJSON string
	if kind == "staff" {
		turnID, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return detail, errAnswerNotFound
		}
		if err := db.QueryRow(ctx, staffAnswerDetailSQL, tenantID, turnID, shops).Scan(
			&detail.ID, &detail.RequestID, &detail.CreatedAt, &detail.ShopID, &detail.Question,
			&detail.Answer, &detail.CitationCount, &detail.FactCount, &detail.IsMock,
			&detail.Behavior, &detail.State, &executionID, &factsJSON); err != nil {
			return detail, errAnswerNotFound
		}
		detail.Behavior = staffAnswerBehavior(detail.State)
		citations, err := s.staffAnswerCitations(ctx, db, tenantID, executionID)
		if err != nil {
			return detail, err
		}
		detail.Citations = citations
	} else {
		var citationsJSON []byte
		if err := db.QueryRow(ctx, customerAnswerDetailSQL, tenantID, id, shops).Scan(
			&detail.ID, &detail.RequestID, &detail.CreatedAt, &detail.ShopID, &detail.Question,
			&detail.Answer, &detail.CitationCount, &detail.FactCount, &detail.IsMock,
			&detail.Behavior, &detail.State, &executionID, &factsJSON, &citationsJSON); err != nil {
			return detail, errAnswerNotFound
		}
		detail.Behavior = normalizeAnswerBehavior(detail.Behavior)
		citations, err := s.customerAnswerCitations(ctx, db, tenantID, citationsJSON)
		if err != nil {
			return detail, err
		}
		detail.Citations = citations
	}
	facts, err := parseAnswerFacts(factsJSON)
	if err != nil {
		return detail, err
	}
	detail.Facts = facts
	return detail, nil
}

func normalizeAnswerBehavior(value string) string {
	return strings.TrimPrefix(value, "REPLY_BEHAVIOR_")
}

func staffAnswerBehavior(state string) string {
	switch state {
	case "DONE":
		return "ANSWER"
	case "ASKING":
		return "CLARIFY"
	case "FAILED":
		return "HANDOFF"
	default:
		return ""
	}
}

func (s *Store) staffAnswerCitations(ctx context.Context, db *pgxpool.Pool, tenantID int64, executionID string) ([]AnswerCitation, error) {
	rows, err := db.Query(ctx, staffAnswerCitationsSQL, tenantID, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	citations := []AnswerCitation{}
	for rows.Next() {
		item := AnswerCitation{}
		if err = rows.Scan(&item.Index, &item.EvidenceID, &item.DocumentID, &item.VersionID,
			&item.Title, &item.Snippet, &item.DisclosureClass, &item.Valid); err != nil {
			return nil, err
		}
		citations = append(citations, item)
	}
	return citations, rows.Err()
}

func (s *Store) customerAnswerCitations(ctx context.Context, db *pgxpool.Pool, tenantID int64, raw []byte) ([]AnswerCitation, error) {
	var saved []struct {
		ID               string `json:"id"`
		Content          string `json:"content"`
		DocumentID       string `json:"document_id"`
		VersionID        string `json:"version_id"`
		Rank             int32  `json:"rank"`
		DisclosureClass  string `json:"disclosure_class"`
		CustomerEligible bool   `json:"customer_eligible"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &saved); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(saved))
	for _, item := range saved {
		if item.DocumentID != "" {
			keys = append(keys, item.DocumentID)
		}
	}
	type documentState struct {
		title, active, status, review, documentStatus string
	}
	documents := map[string]documentState{}
	if len(keys) > 0 {
		rows, err := db.Query(ctx, answerDocumentTitlesSQL, tenantID, keys)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var state documentState
			if err = rows.Scan(&key, &state.title, &state.active, &state.status, &state.review, &state.documentStatus); err != nil {
				return nil, err
			}
			documents[key] = state
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	citations := []AnswerCitation{}
	for _, item := range saved {
		state := documents[item.DocumentID]
		citations = append(citations, AnswerCitation{
			Index: int(item.Rank), EvidenceID: item.ID, DocumentID: item.DocumentID,
			VersionID: item.VersionID, Title: state.title, Snippet: item.Content,
			DisclosureClass: item.DisclosureClass,
			Valid: item.DisclosureClass == "external_allowed" && item.CustomerEligible &&
				state.documentStatus == "active" && state.active == item.VersionID &&
				state.status == "active" && state.review == "approved",
		})
	}
	return citations, nil
}

func parseAnswerFacts(raw string) ([]AnswerFact, error) {
	var saved []struct {
		ToolName   string `json:"tool_name"`
		ValueJSON  string `json:"value_json"`
		ObservedAt string `json:"observed_at"`
		IsMock     bool   `json:"is_mock"`
		ResourceID string `json:"resource_id"`
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &saved); err != nil {
			return nil, err
		}
	}
	facts := []AnswerFact{}
	for _, item := range saved {
		facts = append(facts, AnswerFact{ToolName: item.ToolName, ResourceID: item.ResourceID,
			ObservedAt: item.ObservedAt, ValueJSON: item.ValueJSON, IsMock: item.IsMock})
	}
	return facts, nil
}

// RecordAnswerViewAudit records that a reviewer opened one answer. It never
// stores the answer or question body.
func (s *Store) RecordAnswerViewAudit(ctx context.Context, db *pgxpool.Pool, tenantID, actorID int64, kind, resourceID string) error {
	_, err := db.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,resource_type,resource_id,reason,result)
		VALUES($1,$2,'merchant.answer.view',$3,$4,'review','success')`, tenantID, actorID, kind, resourceID)
	return err
}
