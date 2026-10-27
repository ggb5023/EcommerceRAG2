package admin

import (
	"fmt"
	"time"
)

const (
	importRulesVersion = "v1"
	importAgentModel   = "local-rules-v1"
)

type ImportIssue struct {
	IssueCode       string `json:"issue_code"`
	Severity        string `json:"severity"`
	Snippet         string `json:"snippet"`
	SuggestedAction string `json:"suggested_action"`
}

func issueSnippet(doc ParsedDocument) string {
	snippet := doc.Snippet
	if snippet == "" {
		snippet = doc.Title
	}
	runes := []rune(snippet)
	if len(runes) > 200 {
		snippet = string(runes[:200])
	}
	return snippet
}

// precheckDocuments is the v1 pure rule set. It has no database, RPC or model
// dependency; duplicate detection receives the already-queried hash set.
func precheckDocuments(docs []ParsedDocument, businessDate time.Time, existingHashes map[string]bool) [][]ImportIssue {
	result := make([][]ImportIssue, len(docs))
	for index, doc := range docs {
		issues := make([]ImportIssue, 0, 2)
		snippet := issueSnippet(doc)
		if doc.License == "" && doc.AuthorizationReference == "" {
			issues = append(issues, ImportIssue{IssueCode: "missing_source", Severity: "block", Snippet: snippet,
				SuggestedAction: "补充可引用来源后重新上传"})
		}
		if (doc.EffectiveFrom != "" && doc.EffectiveFrom < businessDate.Format("2006-01-02")) ||
			(doc.EffectiveTo != "" && doc.EffectiveTo < businessDate.Format("2006-01-02")) {
			issues = append(issues, ImportIssue{IssueCode: "expired_content", Severity: "block", Snippet: snippet,
				SuggestedAction: "更新生效期或替换内容"})
		}
		if existingHashes[doc.SourceHash] {
			issues = append(issues, ImportIssue{IssueCode: "duplicate_import", Severity: "warn", Snippet: snippet,
				SuggestedAction: "确认是否覆盖，或撤回旧批次"})
		}
		if doc.DisclosureClass == "external_allowed" &&
			(doc.License == "" || doc.AuthorizationReference == "" || doc.CheckedAt == "") {
			issues = append(issues, ImportIssue{IssueCode: "missing_disclosure_basis", Severity: "block", Snippet: snippet,
				SuggestedAction: "补充 license、authorization.reference 与 checked_at 后重新上传"})
		}
		result[index] = issues
	}
	return result
}

func issueCounts(issues [][]ImportIssue) (int, int) {
	blocks, warnings := 0, 0
	for _, row := range issues {
		for _, issue := range row {
			if issue.Severity == "block" {
				blocks++
			} else if issue.Severity == "warn" {
				warnings++
			}
		}
	}
	return blocks, warnings
}

func parseBusinessDate(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("business_date must be an ISO date")
	}
	return parsed, nil
}
