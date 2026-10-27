package admin

import (
	"testing"
	"time"
)

func reviewInput() ReviewSuggestionInput {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	return ReviewSuggestionInput{
		VersionID: 7, ContentHash: "a", BusinessDate: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		EffectiveFrom: &from, EffectiveTo: &to, DisclosureClass: "external_allowed",
		Chunks:        []ReviewChunk{{ID: "db-chunk-1", Hash: "hash-1", DisclosureClass: "external_allowed"}},
		Evidence:      []ReviewEvidence{{ChunkID: "db-chunk-1", Hash: "hash-1"}},
		Authorization: ReviewAuthorization{Basis: "license", Reference: "ref-1", CheckedAt: "2026-10-01"},
	}
}

func TestReviewSuggestionApproved(t *testing.T) {
	result := evaluateReviewSuggestion(reviewInput())
	if result.Decision != "approved" || len(result.Reasons) != 0 || len(result.EvidenceChunkIDs) != 1 {
		t.Fatalf("approved result=%#v", result)
	}
}

func TestReviewSuggestionCitationBinding(t *testing.T) {
	for _, test := range []struct {
		name     string
		evidence []ReviewEvidence
		code     string
	}{
		{"empty", nil, "missing_citation"},
		{"foreign", []ReviewEvidence{{ChunkID: "db-chunk-9", Hash: "hash-9"}}, "unbound_chunk"},
		{"hash", []ReviewEvidence{{ChunkID: "db-chunk-1", Hash: "other"}}, "unbound_chunk"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := reviewInput()
			input.Evidence = test.evidence
			result := evaluateReviewSuggestion(input)
			if result.Decision != "needs_revision" || len(result.Reasons) != 1 || result.Reasons[0].Code != test.code {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestReviewSuggestionEffectiveCoverage(t *testing.T) {
	after := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		from *time.Time
		to   *time.Time
		bad  bool
	}{
		{"in-range", nil, nil, false},
		{"starts-after", &after, nil, true},
		{"ends-before", nil, &before, true},
		{"ends-on-date", nil, &[]time.Time{time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)}[0], false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := reviewInput()
			input.EffectiveFrom, input.EffectiveTo = test.from, test.to
			result := evaluateReviewSuggestion(input)
			got := len(result.Reasons) == 1 && result.Reasons[0].Code == "effective_expired"
			if got != test.bad {
				t.Fatalf("bad=%v result=%#v", test.bad, result)
			}
		})
	}
}

func TestReviewSuggestionDisclosureConsistency(t *testing.T) {
	input := reviewInput()
	input.Chunks = append(input.Chunks, ReviewChunk{ID: "db-chunk-2", Hash: "hash-2", DisclosureClass: "internal_only"})
	input.Evidence = append(input.Evidence, ReviewEvidence{ChunkID: "db-chunk-2", Hash: "hash-2"})
	result := evaluateReviewSuggestion(input)
	if result.Decision != "needs_revision" || result.Reasons[0].Code != "disclosure_mismatch" {
		t.Fatalf("result=%#v", result)
	}
	internal := reviewInput()
	internal.DisclosureClass = "internal_only"
	internal.Chunks[0].DisclosureClass = "internal_only"
	if result = evaluateReviewSuggestion(internal); result.Decision != "approved" {
		t.Fatalf("internal result=%#v", result)
	}
}

func TestReviewSuggestionAuthorization(t *testing.T) {
	for _, authorization := range []ReviewAuthorization{{Reference: "ref", CheckedAt: "2026-10-01"}, {Basis: "license", CheckedAt: "2026-10-01"}, {Basis: "license", Reference: "ref"}} {
		input := reviewInput()
		input.Authorization = authorization
		result := evaluateReviewSuggestion(input)
		if result.Decision != "needs_revision" || result.Reasons[0].Code != "missing_authorization" {
			t.Fatalf("result=%#v", result)
		}
	}
}
