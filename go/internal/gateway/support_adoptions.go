package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5"
)

const (
	adoptionSourceID   = "staff-suggestions"
	adoptionSourceName = "客服建议采纳"
	adoptionPipeline   = "staff-adoption-v1"
	adoptionMaxTitle   = 120
)

type adoptionPackage struct {
	DocumentID string
	Manifest   string
	Markdown   string
	File       *ragv1.PackageFile
	ManifestSHA string
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// buildAdoptionPackage turns a validated suggestion into a deterministic
// one-document markdown package. The shop is part of the id because
// document.logical_key is unique per tenant, not per shop.
func buildAdoptionPackage(shopID, question, answer string, citations []suggestionCitation) adoptionPackage {
	title := strings.Join(strings.Fields(question), " ")
	if title == "" {
		title = "客服建议知识草稿"
	}
	if runes := []rune(title); len(runes) > adoptionMaxTitle {
		title = string(runes[:adoptionMaxTitle])
	}
	var body strings.Builder
	body.WriteString("# ")
	body.WriteString(title)
	body.WriteString("\n\n## 顾客问题\n\n")
	body.WriteString(strings.TrimSpace(question))
	body.WriteString("\n\n## 建议回答\n\n")
	body.WriteString(strings.TrimSpace(answer))
	body.WriteString("\n\n## 引用来源\n\n")
	for index, item := range citations {
		fmt.Fprintf(&body, "%d. %s / %s / %s\n", index+1, item.document, item.version, item.id)
	}
	markdown := body.String()
	documentID := "staff-faq-" + sha256Hex([]byte(shopID+"\x00"+markdown))[:24]
	sha := sha256Hex([]byte(markdown))
	manifest := fmt.Sprintf(`schema_version: 1
source:
  id: %s
  name: %s
pipeline_version: %s
documents:
  - document_id: %s
    title: %s
    path: documents/%s.md
    format: markdown
    doc_type: faq
    disclosure_class: internal_only
    sha256: %s
`, adoptionSourceID, adoptionSourceName, adoptionPipeline, documentID,
		strconv.Quote(title), documentID, sha)
	return adoptionPackage{
		DocumentID: documentID, Manifest: manifest, Markdown: markdown,
		ManifestSHA: sha256Hex([]byte(manifest)),
		File:        &ragv1.PackageFile{Path: "documents/" + documentID + ".md", Content: []byte(markdown)},
	}
}

// adoptSuggestion submits a reviewed-staff suggestion as an internal knowledge
// draft through the existing ingest pipeline. It never reviews or publishes.
func (g *gateway) adoptSuggestion(w http.ResponseWriter, r *http.Request) {
	if !g.requireWriteRole(w) || !g.authorizeHTTP(w, r) {
		return
	}
	requestID := strings.TrimSpace(r.PathValue("id"))
	if requestID == "" {
		httpapi.WriteError(w, http.StatusNotFound, "suggestion_not_found", "suggestion not found")
		return
	}
	ctx := r.Context()
	tenantID, userID := g.session.TenantID, g.session.UserID
	shopID := g.session.Shop
	if shopID == "" {
		httpapi.WriteError(w, http.StatusForbidden, "suggestion_not_adoptable", "suggestion is outside the current shop")
		return
	}

	tx, err := g.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption is unavailable")
		return
	}
	defer tx.Rollback(ctx)
	if err = lockSuggestionCopy(ctx, tx, "adopt:"+requestID); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption is unavailable")
		return
	}
	turnKind, conversationKind, status, found, err := loadSuggestionForCopy(ctx, tx, tenantID, userID, shopID, requestID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption is unavailable")
		return
	}
	if !found || turnKind != "suggestion" || conversationKind != "suggestion" {
		httpapi.WriteError(w, http.StatusNotFound, "suggestion_not_found", "suggestion not found")
		return
	}
	if status != "DONE" {
		httpapi.WriteError(w, http.StatusForbidden, "suggestion_not_adoptable", "suggestion has not completed")
		return
	}
	result, err := g.executionResult(ctx, requestID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption is unavailable")
		return
	}
	answer, canCopy, blocked, _ := suggestionReplyParts(result)
	if mock, _ := result["is_mock"].(bool); mock || !canCopy || strings.TrimSpace(answer) == "" {
		_ = g.appendAdoptionAudit(ctx, tenantID, userID, requestID, "denied", blocked, map[string]any{"citation_count": 0})
		httpapi.WriteError(w, http.StatusForbidden, "suggestion_not_adoptable", "suggestion is not adoptable")
		return
	}
	_, _, _, resultCitations := suggestionReplyParts(result)
	citationCount, allowed, err := g.suggestionCitationsAllowed(ctx, tx, tenantID, requestID, len(resultCitations))
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption is unavailable")
		return
	}
	if !allowed || citationCount == 0 {
		_ = g.appendAdoptionAudit(ctx, tenantID, userID, requestID, "denied", "citations_not_allowed", map[string]any{"citation_count": citationCount})
		httpapi.WriteError(w, http.StatusForbidden, "suggestion_not_adoptable", "suggestion citations are not available")
		return
	}
	citations, err := loadSuggestionCopyCitations(ctx, tx, tenantID, requestID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption is unavailable")
		return
	}
	question, err := loadSuggestionQuestion(ctx, tx, tenantID, requestID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption is unavailable")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption is unavailable")
		return
	}

	if jobID, adopted, lookupErr := suggestionAdoptionJob(ctx, g.db, tenantID, userID, requestID); lookupErr != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption is unavailable")
		return
	} else if adopted {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{
			"job_id": jobID, "document_id": "", "status": "pending", "already_adopted": true,
		})
		return
	}

	pkg := buildAdoptionPackage(shopID, question, answer, citations)
	jobID, err := g.submitAdoptionPackage(ctx, requestID, shopID, pkg)
	if err != nil {
		_ = g.appendAdoptionAudit(ctx, tenantID, userID, requestID, "failed", "adoption_submit_failed", map[string]any{"citation_count": citationCount})
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "the knowledge draft could not be queued")
		return
	}
	if err = g.appendAdoptionAudit(ctx, tenantID, userID, requestID, "success", "",
		map[string]any{"job_id": jobID, "document_id": pkg.DocumentID,
			"manifest_sha256": pkg.ManifestSHA, "citation_count": citationCount,
			"disclosure_class": "internal_only", "source_id": adoptionSourceID}); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "adoption_unavailable", "adoption audit is unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{
		"job_id": jobID, "document_id": pkg.DocumentID, "manifest_sha256": pkg.ManifestSHA,
		"status": "pending", "already_adopted": false,
	})
}


func (g *gateway) submitAdoptionPackage(ctx context.Context, requestID, shopID string, pkg adoptionPackage) (string, error) {
	scope, err := g.authority().IssueScopeForShop(ctx, httpapi.NewRequestID(), shopID)
	if err != nil {
		return "", err
	}
	scope.ScopeAudience = "ingest_adoption"
	scope.ScopeSignature = hex.EncodeToString(auth.MAC(g.signingKey, scope))
	if g.authority().RegisterScopeIdentity != nil {
		if err = g.authority().RegisterScopeIdentity(scope); err != nil {
			return "", err
		}
	}
	response, err := g.ingest.Submit(ctx, &ragv1.SubmitRequest{
		Context: scope, IdempotencyKey: "staff-adoption:" + shopID + ":" + pkg.ManifestSHA,
		ManifestYaml: pkg.Manifest, Files: []*ragv1.PackageFile{pkg.File},
		TenantDbId: strconv.FormatInt(g.session.TenantID, 10),
		UserDbId:   strconv.FormatInt(g.session.UserID, 10), ShopId: shopID,
	})
	if err != nil {
		return "", err
	}
	return response.GetJobId(), nil
}


// appendAdoptionAudit writes one admin_audit row in its own transaction. The
// body (question/answer/snippets) is never stored.
func (g *gateway) appendAdoptionAudit(ctx context.Context, tenantID, userID int64, requestID, result, reason string, metadata map[string]any) error {
	tx, err := g.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = appendSuggestionAdoptionAudit(ctx, tx, tenantID, userID, requestID, result, reason, metadata); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
