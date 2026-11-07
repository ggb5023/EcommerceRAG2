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

// explicitOrderReferencePattern matches a literal only when it follows an
// explicit order cue. Bare identifiers elsewhere in the query are ignored.
var explicitOrderReferencePattern = regexp.MustCompile(`(?i)(?:订单号|订单编号|\border\s+id)\s*(?:是|为|[:：=#])?\s*([A-Za-z0-9][A-Za-z0-9_-]{0,63})`)

// ExplicitOrderReferences returns the literals written immediately after an
// explicit order cue (订单号 / 订单编号 / order id). It deliberately does not
// scan bare identifiers: a SKU or an unrelated resource value is not an order
// reference just because it has an identifier shape.
func ExplicitOrderReferences(query string) []string {
	matches := explicitOrderReferencePattern.FindAllStringSubmatch(query, -1)
	references := make([]string, 0, len(matches))
	for _, match := range matches {
		references = append(references, match[1])
	}
	return references
}

// IsOrderIdentifier reports whether s has the shape of an order identifier.
// The regex stays private: exporting it would invite new copies elsewhere.
func IsOrderIdentifier(s string) bool { return orderIDPattern.MatchString(s) }

// ResourceTokens returns the identifier-shaped tokens in s.
func ResourceTokens(s string) []string { return queryResourcePattern.FindAllString(s, -1) }

// OrderFamilyReferences returns the bare tokens in query that belong to the same
// identifier family as bound: same first segment (case-insensitive), same
// segment count, and the same character class per segment (letters, digits or
// mixed). A token only participates when it has letters, digits and at least
// one '-' or '_' separator; a purely numeric or separator-free bound has no
// family and returns nil. Values in ignore (model-reported entities) and the
// bound itself are never returned.
//
// The rule is deliberately narrow: it catches "ORD-2609-0001" typed for the
// bound "ORD-2609-0005" without turning every identifier-shaped token into an
// order reference (decision 0043).
func OrderFamilyReferences(query, bound string, ignore []string) []string {
	boundSegments, ok := familySegments(bound)
	if !ok {
		return nil
	}
	excluded := make(map[string]struct{}, len(ignore)+1)
	excluded[strings.ToLower(bound)] = struct{}{}
	for _, value := range ignore {
		excluded[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	var references []string
	for _, token := range queryResourcePattern.FindAllString(query, -1) {
		segments, ok := familySegments(token)
		if !ok || !sameIdentifierFamily(boundSegments, segments) {
			continue
		}
		if _, skip := excluded[strings.ToLower(token)]; skip {
			continue
		}
		references = append(references, token)
	}
	return references
}

// familySegments splits an identifier on '-' and '_' and reports whether it can
// take part in family detection: identifier shape, letters and digits present,
// at least one separator.
func familySegments(value string) ([]string, bool) {
	if !orderIDPattern.MatchString(value) {
		return nil, false
	}
	var letters, digits bool
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			letters = true
		case r >= '0' && r <= '9':
			digits = true
		}
	}
	if !letters || !digits || !strings.ContainsAny(value, "-_") {
		return nil, false
	}
	segments := strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '_' })
	if len(segments) < 2 {
		return nil, false
	}
	return segments, true
}

// sameIdentifierFamily compares a candidate against the bound segments. The
// first segment must match case-insensitively; every segment must keep the
// bound's character class so a same-prefix price or quantity is caught while an
// unrelated identifier is not.
func sameIdentifierFamily(bound, candidate []string) bool {
	if len(bound) != len(candidate) || !strings.EqualFold(bound[0], candidate[0]) {
		return false
	}
	for index := range bound {
		class := identifierSegmentClass(bound[index])
		if class == "" || identifierSegmentClass(candidate[index]) != class {
			return false
		}
	}
	return true
}

func identifierSegmentClass(segment string) string {
	var letters, digits bool
	for _, r := range segment {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			letters = true
		case r >= '0' && r <= '9':
			digits = true
		default:
			return ""
		}
	}
	switch {
	case letters && digits:
		return "mixed"
	case letters:
		return "letters"
	case digits:
		return "digits"
	default:
		return ""
	}
}
