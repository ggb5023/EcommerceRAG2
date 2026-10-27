package admin

import "time"

const reviewSuggestionRulesVersion = "v1"

type ReviewChunk struct {
	ID              string
	Hash            string
	DisclosureClass string
}

type ReviewEvidence struct {
	ChunkID string
	Hash    string
}

type ReviewAuthorization struct {
	Basis     string
	Reference string
	CheckedAt string
}

type ReviewSuggestionInput struct {
	VersionID       int64
	ContentHash     string
	BusinessDate    time.Time
	EffectiveFrom   *time.Time
	EffectiveTo     *time.Time
	DisclosureClass string
	Chunks          []ReviewChunk
	Evidence        []ReviewEvidence
	Authorization   ReviewAuthorization
}

type ReviewReason struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type ReviewSuggestion struct {
	Decision         string         `json:"decision"`
	Reasons          []ReviewReason `json:"reasons"`
	EvidenceChunkIDs []string       `json:"evidence_chunk_ids"`
}

func reviewDayEqual(left, right time.Time) bool {
	ly, lm, ld := left.Date()
	ry, rm, rd := right.Date()
	return ly == ry && lm == rm && ld == rd
}

func reviewDayBefore(left, right time.Time) bool {
	ly, lm, ld := left.Date()
	ry, rm, rd := right.Date()
	return time.Date(ly, lm, ld, 0, 0, 0, 0, time.UTC).Before(time.Date(ry, rm, rd, 0, 0, 0, 0, time.UTC))
}

// evaluateReviewSuggestion applies the four deterministic L2 criteria. It does
// not read the database or write reviewed_*; evidence must bind to this version.
func evaluateReviewSuggestion(input ReviewSuggestionInput) ReviewSuggestion {
	chunks := make(map[string]ReviewChunk, len(input.Chunks))
	for _, chunk := range input.Chunks {
		chunks[chunk.ID] = chunk
	}
	reasons := make([]ReviewReason, 0, 4)
	evidenceIDs := make([]string, 0, len(input.Evidence))
	seen := map[string]bool{}
	if len(input.Evidence) == 0 {
		reasons = append(reasons, ReviewReason{Code: "missing_citation", Detail: "no evidence chunk is bound"})
	}
	for _, evidence := range input.Evidence {
		if seen[evidence.ChunkID] {
			continue
		}
		seen[evidence.ChunkID] = true
		evidenceIDs = append(evidenceIDs, evidence.ChunkID)
		chunk, ok := chunks[evidence.ChunkID]
		if !ok {
			reasons = append(reasons, ReviewReason{Code: "unbound_chunk", Detail: "evidence chunk does not belong to this version: " + evidence.ChunkID})
			continue
		}
		if evidence.Hash == "" || evidence.Hash != chunk.Hash {
			reasons = append(reasons, ReviewReason{Code: "unbound_chunk", Detail: "evidence chunk hash does not match this version: " + evidence.ChunkID})
		}
	}
	if input.EffectiveFrom != nil && reviewDayBefore(input.BusinessDate, *input.EffectiveFrom) {
		reasons = append(reasons, ReviewReason{Code: "effective_expired", Detail: "effective_from is after business_date"})
	}
	if input.EffectiveTo != nil && reviewDayBefore(*input.EffectiveTo, input.BusinessDate) && !reviewDayEqual(*input.EffectiveTo, input.BusinessDate) {
		reasons = append(reasons, ReviewReason{Code: "effective_expired", Detail: "effective_to is before business_date"})
	}
	if input.DisclosureClass == "external_allowed" {
		for _, chunk := range input.Chunks {
			if chunk.DisclosureClass != "external_allowed" {
				reasons = append(reasons, ReviewReason{Code: "disclosure_mismatch", Detail: "external version contains a non-external chunk: " + chunk.ID})
			}
		}
	}
	if input.Authorization.Basis == "" || input.Authorization.Reference == "" || input.Authorization.CheckedAt == "" {
		reasons = append(reasons, ReviewReason{Code: "missing_authorization", Detail: "document authorization basis/reference/checked_at is incomplete"})
	}
	decision := "approved"
	if len(reasons) > 0 {
		decision = "needs_revision"
	}
	return ReviewSuggestion{Decision: decision, Reasons: reasons, EvidenceChunkIDs: evidenceIDs}
}
