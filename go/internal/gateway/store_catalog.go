package gateway

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/domain"
	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
	"github.com/jackc/pgx/v5/pgxpool"
)

const catalogFilterMaxValues = 20

type normalizedCatalogFilter struct {
	skuCodes       []string
	colors         []string
	sizeLabels     []string
	sizeChartCodes []string
	attributes     map[string]string
}

func normalizeCatalogFilter(filter *ragv1.CatalogFilter) (normalizedCatalogFilter, error) {
	result := normalizedCatalogFilter{attributes: map[string]string{}}
	if filter == nil {
		return result, nil
	}
	normalize := func(values []string, lower bool) ([]string, error) {
		if len(values) > catalogFilterMaxValues {
			return nil, fmt.Errorf("catalog_filter_values_too_many")
		}
		seen := map[string]struct{}{}
		out := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || len(value) > 128 {
				return nil, fmt.Errorf("catalog_filter_value_invalid")
			}
			if lower {
				value = strings.ToLower(value)
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
		sort.Strings(out)
		return out, nil
	}
	var err error
	if result.skuCodes, err = normalize(filter.GetSkuCodes(), true); err != nil {
		return result, err
	}
	if result.colors, err = normalize(filter.GetColors(), true); err != nil {
		return result, err
	}
	if result.sizeLabels, err = normalize(filter.GetSizeLabels(), true); err != nil {
		return result, err
	}
	if result.sizeChartCodes, err = normalize(filter.GetSizeChartCodes(), true); err != nil {
		return result, err
	}
	for key, value := range filter.GetAttributes() {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if !domain.CatalogAttributeAllowed(key) || value == "" || len(value) > 200 {
			return result, fmt.Errorf("catalog_filter_attribute_invalid")
		}
		result.attributes[key] = value
	}
	return result, nil
}

func catalogFilterSQL(tenantID int64, shopID, businessDate string,
	filter normalizedCatalogFilter) (string, []any) {
	args := []any{tenantID, shopID, businessDate}
	conditions := []string{
		"cs.tenant_id=$1",
		"cs.shop_id=$2",
		"v.status='active'",
		"d.active_version_id=v.id",
		"v.review_status='approved'",
		"d.status='active'",
		"(v.effective_from IS NULL OR v.effective_from <= $3::date)",
		"(v.effective_to IS NULL OR v.effective_to > $3::date)",
	}
	add := func(column string, values []string) {
		if len(values) == 0 {
			return
		}
		args = append(args, values)
		conditions = append(conditions, fmt.Sprintf("lower(cs.%s) = ANY($%d::text[])", column, len(args)))
	}
	add("sku_code", filter.skuCodes)
	add("color", filter.colors)
	add("size_label", filter.sizeLabels)
	add("size_chart_code", filter.sizeChartCodes)
	keys := make([]string, 0, len(filter.attributes))
	for key := range filter.attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, key, filter.attributes[key])
		conditions = append(conditions,
			fmt.Sprintf("cs.attributes ->> $%d::text = $%d", len(args)-1, len(args)))
	}
	query := "SELECT EXISTS (SELECT 1 FROM catalog_sku cs " +
		"JOIN document_version v ON v.tenant_id=cs.tenant_id AND v.id=cs.version_id " +
		"JOIN document d ON d.tenant_id=v.tenant_id AND d.id=v.document_id " +
		"JOIN source s ON s.tenant_id=d.tenant_id AND s.id=d.source_id " +
		"WHERE " + strings.Join(conditions, " AND ") + ")"
	return query, args
}

func catalogFilterMatches(ctx context.Context, db *pgxpool.Pool, tenantID int64,
	shopID, businessDate string, filter *ragv1.CatalogFilter) (bool, error) {
	if _, err := time.Parse("2006-01-02", businessDate); err != nil {
		return false, fmt.Errorf("catalog_filter_business_date_invalid")
	}
	normalized, err := normalizeCatalogFilter(filter)
	if err != nil {
		return false, err
	}
	if len(normalized.skuCodes) == 0 && len(normalized.colors) == 0 &&
		len(normalized.sizeLabels) == 0 && len(normalized.sizeChartCodes) == 0 &&
		len(normalized.attributes) == 0 {
		return true, nil
	}
	query, args := catalogFilterSQL(tenantID, shopID, businessDate, normalized)
	var exists bool
	if err := db.QueryRow(ctx, query, args...).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}
