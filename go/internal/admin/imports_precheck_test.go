package admin

import (
	"testing"
	"time"
)

func TestPrecheckV1Rules(t *testing.T) {
	businessDate := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	docs := []ParsedDocument{
		{DocumentID: "missing", Title: "Missing", SourceHash: "h1"},
		{DocumentID: "expired", Title: "Expired", License: "internal", EffectiveTo: "2026-10-26", SourceHash: "h2"},
		{DocumentID: "duplicate", Title: "Duplicate", License: "internal", SourceHash: "h3"},
		{DocumentID: "disclosure", Title: "Disclosure", DisclosureClass: "external_allowed", SourceHash: "h4"},
		{DocumentID: "clean", Title: "Clean", License: "internal", DisclosureClass: "internal_only", SourceHash: "h5"},
	}
	issues := precheckDocuments(docs, businessDate, map[string]bool{"h3": true})
	if len(issues[0]) != 1 || issues[0][0].IssueCode != "missing_source" || issues[0][0].Severity != "block" {
		t.Fatalf("missing_source: %#v", issues[0])
	}
	if len(issues[1]) != 1 || issues[1][0].IssueCode != "expired_content" || issues[1][0].Severity != "block" {
		t.Fatalf("expired_content: %#v", issues[1])
	}
	if len(issues[2]) != 1 || issues[2][0].IssueCode != "duplicate_import" || issues[2][0].Severity != "warn" {
		t.Fatalf("duplicate_import: %#v", issues[2])
	}
	if len(issues[3]) != 2 || issues[3][1].IssueCode != "missing_disclosure_basis" || issues[3][1].Severity != "block" {
		t.Fatalf("missing_disclosure_basis: %#v", issues[3])
	}
	if len(issues[4]) != 0 {
		t.Fatalf("clean document flagged: %#v", issues[4])
	}
	blocks, warnings := issueCounts(issues)
	if blocks != 4 || warnings != 1 {
		t.Fatalf("counts blocks=%d warnings=%d", blocks, warnings)
	}
}
