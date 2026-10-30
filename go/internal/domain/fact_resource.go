package domain

import (
	"math"
	"regexp"
	"slices"
	"strings"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

// Private locators: they are rules, not API. Only the entry points below are
// exported so the gateway can keep thin wrappers.
var orderIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var queryResourcePattern = regexp.MustCompile(`[A-Za-z0-9_-]+`)

// RoutedOrderResource extracts an explicit order id from the routed entities.
func RoutedOrderResource(response *ragv1.UnderstandResponse, query string) (string, bool) {
	if response == nil {
		return "", false
	}
	ids := make([]string, 0, 1)
	for _, entity := range response.Entities {
		if entity.GetName() != "order_id" {
			continue
		}
		confidence := entity.GetConfidence()
		if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0.5 || confidence > 1 {
			return "", false
		}
		id := strings.TrimSpace(entity.GetValue())
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 || !orderIDPattern.MatchString(ids[0]) {
		return "", false
	}
	if !slices.Contains(queryResourcePattern.FindAllString(query, -1), ids[0]) {
		return "", false
	}
	return ids[0], true
}

// IsOrderIdentifier reports whether s has the shape of an order identifier.
// The regex stays private: exporting it would invite new copies elsewhere.
func IsOrderIdentifier(s string) bool { return orderIDPattern.MatchString(s) }

// ResourceTokens returns the identifier-shaped tokens in s.
func ResourceTokens(s string) []string { return queryResourcePattern.FindAllString(s, -1) }
