package gateway

import (
	"errors"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

// Rank is the citation label given to generation, not an array offset. Scope
// and context filters can leave gaps; snapshots and citations retain the label.
func validateEvidenceRanks(items []*ragv1.Evidence) error {
	ids, ranks := map[string]bool{}, map[int32]bool{}
	for _, item := range items {
		if item == nil || item.Id == "" || item.Rank <= 0 || ids[item.Id] || ranks[item.Rank] {
			return errors.New("invalid evidence citation labels")
		}
		ids[item.Id], ranks[item.Rank] = true, true
	}
	return nil
}
