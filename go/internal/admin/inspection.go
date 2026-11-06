package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

// The inspection window and cluster floor are defined here and nowhere else.
// Tests read these constants instead of repeating the numbers.
const (
	INSPECTION_WINDOW_DAYS       = 30
	INSPECTION_MIN_CLUSTER_TURNS = 2
	inspectionRuleVersion        = "v1"
)

const (
	inspectionKindExpired     = "expired_not_removed"
	inspectionKindNewer       = "newer_version_conflict"
	inspectionKindUnanswered  = "unanswered_high_frequency"
	inspectionKindCitation    = "citation_invalid"
	inspectionSeverityHigh    = "high"
	inspectionSeverityMedium  = "medium"
	inspectionSubjectVersion  = "document_version"
	inspectionSubjectDocument = "document"
	inspectionSubjectGap      = "gap"
	inspectionSubjectAnswer   = "gateway_execution"
	inspectionSubjectCustomer = "customer_message"
)

var inspectionKinds = []string{
	inspectionKindExpired,
	inspectionKindNewer,
	inspectionKindUnanswered,
	inspectionKindCitation,
}

type inspectionFinding struct {
	TenantID    int64
	ShopID      string
	Kind        string
	Severity    string
	SubjectType string
	SubjectID   string
	Fingerprint string
	Evidence    map[string]any
}

type inspectionKindSummary struct {
	Inserted int `json:"inserted"`
	Updated  int `json:"updated"`
	Closed   int `json:"closed"`
}

type inspectionRunSummary struct {
	RunID  string                           `json:"run_id"`
	ByKind map[string]inspectionKindSummary `json:"by_kind"`
}

// inspectionFingerprint is the stable identity of one finding. A closed row
// does not participate in the open-only unique index, so a later detection
// inserts a fresh open row and reopens the finding.
func inspectionFingerprint(tenantID int64, kind, subjectType, subjectID string) string {
	return fmt.Sprintf("%d/%s/%s/%s/%s", tenantID, kind, subjectType, subjectID, inspectionRuleVersion)
}

func newInspectionFinding(tenantID int64, shopID, kind, severity, subjectType, subjectID string, evidence map[string]any) inspectionFinding {
	return inspectionFinding{
		TenantID: tenantID, ShopID: shopID, Kind: kind, Severity: severity,
		SubjectType: subjectType, SubjectID: subjectID,
		Fingerprint: inspectionFingerprint(tenantID, kind, subjectType, subjectID),
		Evidence:    evidence,
	}
}

// runInspections evaluates the four v1 rules for every shop in scope, then
// upserts and closes reports in one transaction. It never writes a business
// table and never changes publication state.
func (g *Handler) runInspections(ctx context.Context, scope adminScope) (inspectionRunSummary, error) {
	shops, err := adminStore.InspectionShops(ctx, g.DB, scope.TenantID, scope.shops, scope.tenantWide)
	if err != nil {
		return inspectionRunSummary{}, err
	}
	now := time.Now()
	from := now.Add(-time.Duration(INSPECTION_WINDOW_DAYS) * 24 * time.Hour)
	collected := make(map[string][]inspectionFinding, len(inspectionKinds))
	for _, shop := range shops {
		businessDate, err := auth.ShopBusinessDate(ctx, g.DB, scope.TenantID, shop.ID)
		if err != nil {
			return inspectionRunSummary{}, err
		}
		expired, err := adminStore.ExpiredVersions(ctx, g.DB, scope.TenantID, shop.ID, businessDate)
		if err != nil {
			return inspectionRunSummary{}, err
		}
		collected[inspectionKindExpired] = append(collected[inspectionKindExpired],
			expiredVersionFindings(scope.TenantID, shop.ID, businessDate, expired)...)

		newer, err := adminStore.NewerVersionConflicts(ctx, g.DB, scope.TenantID, shop.ID, from)
		if err != nil {
			return inspectionRunSummary{}, err
		}
		collected[inspectionKindNewer] = append(collected[inspectionKindNewer],
			newerVersionFindings(scope.TenantID, shop.ID, newer)...)

		gaps, err := adminStore.InspectionKnowledgeGaps(ctx, g.DB, scope.TenantID, shop.ID, from, INSPECTION_MIN_CLUSTER_TURNS)
		if err != nil {
			return inspectionRunSummary{}, err
		}
		collected[inspectionKindUnanswered] = append(collected[inspectionKindUnanswered],
			gapFindings(scope.TenantID, shop.ID, gaps)...)

		citations, err := g.citationInspectionFindings(ctx, scope.TenantID, g.Session.Tenant, shop.ID)
		if err != nil {
			return inspectionRunSummary{}, err
		}
		collected[inspectionKindCitation] = append(collected[inspectionKindCitation], citations...)
	}
	return g.persistInspectionRun(ctx, scope, collected, now)
}

func expiredVersionFindings(tenantID int64, shopID, businessDate string, rows []expiredVersionRow) []inspectionFinding {
	items := make([]inspectionFinding, 0, len(rows))
	for _, row := range rows {
		items = append(items, newInspectionFinding(tenantID, shopID, inspectionKindExpired, inspectionSeverityHigh,
			inspectionSubjectVersion, row.VersionID, map[string]any{
				"document_id":   row.DocumentID,
				"logical_key":   row.LogicalKey,
				"title":         row.Title,
				"version_id":    row.VersionID,
				"effective_to":  row.EffectiveTo,
				"business_date": businessDate,
				"reason":        "effective_to_before_business_date",
			}))
	}
	return items
}

func newerVersionFindings(tenantID int64, shopID string, rows []newerVersionConflictRow) []inspectionFinding {
	items := make([]inspectionFinding, 0, len(rows))
	for _, row := range rows {
		items = append(items, newInspectionFinding(tenantID, shopID, inspectionKindNewer, inspectionSeverityMedium,
			inspectionSubjectDocument, fmt.Sprintf("%d", row.DocumentID), map[string]any{
				"document_id":        row.DocumentID,
				"logical_key":        row.LogicalKey,
				"title":              row.Title,
				"active_version_id":  row.VersionID,
				"newer_version_ids":  row.NewVersionIDs,
				"employee_citations": row.EmployeeRefs,
				"customer_citations": row.CustomerRefs,
				"window_days":        INSPECTION_WINDOW_DAYS,
				"reason":             "newer_version_while_old_is_cited",
			}))
	}
	return items
}

func gapFindings(tenantID int64, shopID string, gaps []KnowledgeGap) []inspectionFinding {
	items := make([]inspectionFinding, 0, len(gaps))
	for _, gap := range gaps {
		// The subject id is namespaced by shop because query clusters are
		// aggregated per shop; the same wording in two shops is two findings.
		subjectID := shopID + ":" + gap.Normalized
		items = append(items, newInspectionFinding(tenantID, shopID, inspectionKindUnanswered, inspectionSeverityMedium,
			inspectionSubjectGap, subjectID, map[string]any{
				"query":             gap.Query,
				"normalized":        gap.Normalized,
				"count":             gap.Count,
				"last_seen":         gap.LastSeen,
				"gap_reason":        gap.Reason,
				"min_cluster_turns": INSPECTION_MIN_CLUSTER_TURNS,
				"reason":            "no_published_material",
			}))
	}
	return items
}

type citationSubject struct {
	Source string
	ID     string
}

// citationInspectionFindings checks every cited evidence snapshot with the same
// auth.CustomerEvidenceAllowed call used by the customer read path. A cited item
// that is revoked, expired or otherwise customer-invisible becomes one finding
// per answer. Answer text and customer wording are never copied into evidence.
func (g *Handler) citationInspectionFindings(ctx context.Context, tenantID int64, tenantExternal, shopID string) ([]inspectionFinding, error) {
	gateway, err := adminStore.InspectionGatewayCitations(ctx, g.DB, tenantID, shopID)
	if err != nil {
		return nil, err
	}
	customer, err := adminStore.InspectionCustomerCitations(ctx, g.DB, tenantID, shopID)
	if err != nil {
		return nil, err
	}
	subjects := make(map[citationSubject][]*ragv1.Evidence)
	for _, row := range gateway {
		key := citationSubject{Source: "employee", ID: row.RequestID}
		subjects[key] = append(subjects[key], &ragv1.Evidence{
			Id: row.EvidenceID, DocumentId: row.DocumentID, VersionId: row.VersionID,
			ShopId: row.ShopID, Content: row.Content, DisclosureClass: row.DisclosureClass,
			CustomerEligible: row.CustomerEligible,
		})
	}
	for _, row := range customer {
		var items []*ragv1.Evidence
		if err := json.Unmarshal(row.Citations, &items); err != nil {
			return nil, err
		}
		subjects[citationSubject{Source: "customer", ID: row.MessageID}] = items
	}
	keys := make([]citationSubject, 0, len(subjects))
	for key := range subjects {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Source != keys[j].Source {
			return keys[i].Source < keys[j].Source
		}
		return keys[i].ID < keys[j].ID
	})

	invalidBySubject := make(map[citationSubject][]map[string]any)
	documentKeys := make(map[string]bool)
	for _, key := range keys {
		seen := make(map[string]bool)
		for _, item := range subjects[key] {
			if item == nil {
				continue
			}
			if item.TenantId != "" && item.TenantId != tenantExternal {
				dedupe := item.Id + "|" + item.VersionId
				if !seen[dedupe] {
					seen[dedupe] = true
					invalidBySubject[key] = append(invalidBySubject[key], map[string]any{
						"evidence_id": item.Id,
						"document_id": item.DocumentId,
						"version_id":  item.VersionId,
						"reason":      "tenant_mismatch",
					})
				}
				if item.DocumentId != "" {
					documentKeys[item.DocumentId] = true
				}
				continue
			}
			item.TenantId = tenantExternal
			if item.ShopId == "" {
				item.ShopId = shopID
			}
			allowed, err := auth.CustomerEvidenceAllowed(ctx, g.DB, tenantID, shopID, item)
			if err != nil {
				return nil, err
			}
			if allowed {
				continue
			}
			dedupe := item.Id + "|" + item.VersionId
			if seen[dedupe] {
				continue
			}
			seen[dedupe] = true
			invalidBySubject[key] = append(invalidBySubject[key], map[string]any{
				"evidence_id": item.Id,
				"document_id": item.DocumentId,
				"version_id":  item.VersionId,
				"reason":      "not_customer_visible",
			})
			if item.DocumentId != "" {
				documentKeys[item.DocumentId] = true
			}
		}
	}
	keysForTitles := make([]string, 0, len(documentKeys))
	for key := range documentKeys {
		keysForTitles = append(keysForTitles, key)
	}
	titles, err := adminStore.InspectionDocumentTitles(ctx, g.DB, tenantID, keysForTitles)
	if err != nil {
		return nil, err
	}
	findings := make([]inspectionFinding, 0, len(invalidBySubject))
	for _, key := range keys {
		invalid := invalidBySubject[key]
		if len(invalid) == 0 {
			continue
		}
		title := ""
		for _, item := range invalid {
			if documentID, ok := item["document_id"].(string); ok {
				if documentTitle, ok := titles[documentID]; ok {
					item["title"] = documentTitle
					if title == "" {
						title = documentTitle
					}
				}
			}
		}
		subjectType := inspectionSubjectAnswer
		if key.Source == "customer" {
			subjectType = inspectionSubjectCustomer
		}
		findings = append(findings, newInspectionFinding(tenantID, shopID, inspectionKindCitation, inspectionSeverityHigh,
			subjectType, key.ID, map[string]any{
				"source":            key.Source,
				"answer_id":         key.ID,
				"title":             title,
				"invalid_citations": invalid,
				"reason":            "cited_evidence_not_customer_visible",
			}))
	}
	return findings, nil
}

func (g *Handler) persistInspectionRun(ctx context.Context, scope adminScope, collected map[string][]inspectionFinding, now time.Time) (inspectionRunSummary, error) {
	summary := inspectionRunSummary{
		RunID:  fmt.Sprintf("inspection-%d", now.UnixNano()),
		ByKind: make(map[string]inspectionKindSummary, len(inspectionKinds)),
	}
	tx, err := g.DB.Begin(ctx)
	if err != nil {
		return summary, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, kind := range inspectionKinds {
		seen := make(map[string]bool)
		keep := make([]string, 0, len(collected[kind]))
		kindSummary := inspectionKindSummary{}
		for _, finding := range collected[kind] {
			if seen[finding.Fingerprint] {
				continue
			}
			seen[finding.Fingerprint] = true
			keep = append(keep, finding.Fingerprint)
			evidence, err := json.Marshal(finding.Evidence)
			if err != nil {
				return summary, err
			}
			inserted, err := adminStore.UpsertInspectionReport(ctx, tx, inspectionReportRow{
				TenantID: finding.TenantID, ShopID: finding.ShopID, Kind: finding.Kind,
				Severity: finding.Severity, SubjectType: finding.SubjectType,
				SubjectID: finding.SubjectID, Fingerprint: finding.Fingerprint, Evidence: evidence,
			})
			if err != nil {
				return summary, err
			}
			if inserted {
				kindSummary.Inserted++
			} else {
				kindSummary.Updated++
			}
		}
		closed, err := adminStore.CloseMissingInspectionReports(ctx, tx, scope.TenantID,
			scope.shops, scope.tenantWide, kind, keep)
		if err != nil {
			return summary, err
		}
		kindSummary.Closed = int(closed)
		summary.ByKind[kind] = kindSummary
	}
	if err := tx.Commit(ctx); err != nil {
		return summary, err
	}
	return summary, nil
}
