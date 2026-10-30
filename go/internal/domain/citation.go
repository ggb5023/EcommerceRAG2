package domain

import (
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"google.golang.org/protobuf/proto"
)

// ValidateCitation reports whether the cited evidence is one of the accepted
// candidates. It is pure: it only compares protobuf messages.
func ValidateCitation(citation *ragv1.Evidence, accepted []*ragv1.Evidence) bool {
	for _, original := range accepted {
		if proto.Equal(original, citation) {
			return true
		}
	}
	return false
}
