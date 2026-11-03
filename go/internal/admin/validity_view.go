package admin

import (
	"context"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/auth"
)

func (g *Handler) shopBusinessDate(ctx context.Context, shop string, cache map[string]string) (string, error) {
	if value, ok := cache[shop]; ok {
		return value, nil
	}
	value, err := auth.ShopBusinessDate(ctx, g.DB, g.Session.TenantID, shop)
	if err != nil {
		return "", err
	}
	cache[shop] = value
	return value, nil
}

func validityFields(from, to *time.Time, businessDate string) (map[string]any, error) {
	status, days, err := validityStatus(to, businessDate)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"effective_from":    validityDateString(from),
		"effective_to":      validityDateString(to),
		"validity_status":   status,
		"days_until_expiry": days,
	}, nil
}

func optionalDate(value any) *time.Time {
	text, ok := value.(string)
	if !ok || text == "" {
		return nil
	}
	parsed, err := time.Parse("2006-01-02", text)
	if err != nil {
		return nil
	}
	return &parsed
}
