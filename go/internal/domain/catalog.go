package domain

import (
	"math"
	"regexp"
	"slices"
	"strings"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

var catalogAttributeName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var catalogAttributeKeys = map[string]struct{}{
	"capacity_ml": {}, "length_cm": {}, "width_cm": {}, "height_cm": {},
	"weight_g": {}, "material": {}, "pattern": {},
}

// CatalogFilterCandidates extracts only explicit, query-backed catalog values.
// It intentionally does not tokenize free text: colors and sizes must arrive as
// named entities, and SKU/chart codes must be identifier tokens in the query.
func CatalogFilterCandidates(response *ragv1.UnderstandResponse, query string) *ragv1.CatalogFilter {
	if response == nil {
		return nil
	}
	filter := &ragv1.CatalogFilter{Attributes: map[string]string{}}
	tokens := ResourceTokens(query)
	for _, entity := range response.Entities {
		if entity == nil {
			continue
		}
		confidence := entity.GetConfidence()
		if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < .5 || confidence > 1 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(entity.GetName()))
		value := strings.TrimSpace(entity.GetValue())
		if value == "" {
			continue
		}
		if name != "attribute" && name != "attributes" && !queryContains(query, value) {
			continue
		}
		switch name {
		case "sku", "sku_code":
			if slices.Contains(tokens, value) {
				filter.SkuCodes = appendUnique(filter.SkuCodes, value)
			}
		case "color", "颜色":
			filter.Colors = appendUnique(filter.Colors, value)
		case "size", "size_label", "尺码":
			filter.SizeLabels = appendUnique(filter.SizeLabels, value)
		case "size_chart_code", "尺码表编码":
			if slices.Contains(tokens, value) {
				filter.SizeChartCodes = appendUnique(filter.SizeChartCodes, value)
			}
		case "attribute", "attributes":
			key, attributeValue, ok := strings.Cut(value, "=")
			key = strings.ToLower(strings.TrimSpace(key))
			attributeValue = strings.TrimSpace(attributeValue)
			if ok && catalogAttributeName.MatchString(key) && attributeValue != "" &&
				queryContains(query, attributeValue) {
				if _, allowed := catalogAttributeKeys[key]; allowed {
					filter.Attributes[key] = attributeValue
				}
			}
		default:
			if _, allowed := catalogAttributeKeys[name]; allowed && queryContains(query, value) {
				filter.Attributes[name] = value
			}
		}
	}
	if len(filter.SkuCodes) == 0 && len(filter.Colors) == 0 && len(filter.SizeLabels) == 0 &&
		len(filter.SizeChartCodes) == 0 && len(filter.Attributes) == 0 {
		return nil
	}
	slices.Sort(filter.SkuCodes)
	slices.Sort(filter.Colors)
	slices.Sort(filter.SizeLabels)
	slices.Sort(filter.SizeChartCodes)
	return filter
}

// CatalogAttributeAllowed reports whether key is a 0034 registered filter key.
func CatalogAttributeAllowed(key string) bool {
	_, ok := catalogAttributeKeys[key]
	return ok
}

func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func queryContains(query, value string) bool {
	return strings.Contains(strings.ToLower(query), strings.ToLower(value))
}
