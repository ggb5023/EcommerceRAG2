package facts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	ragv1 "github.com/ggb5023/EcommerceRAG/go/internal/pb/rag/v1"
)

// HTTPProvider is the Go-owned adapter for a versioned merchant facts API.
// The upstream API never receives Python credentials or an untrusted scope.
type HTTPProvider struct {
	BaseURL string
	Version string
	Client  *http.Client
}

type httpFactResponse struct {
	Version    string          `json:"version"`
	TenantID   string          `json:"tenant_id"`
	ShopID     string          `json:"shop_id"`
	ToolName   string          `json:"tool_name"`
	ValueJSON  json.RawMessage `json:"value_json"`
	Observed   string          `json:"observed_at"`
	IsMock     bool            `json:"is_mock"`
	ResourceID string          `json:"resource_id"`
}

func (p HTTPProvider) Lookup(ctx context.Context, tool string, scope *ragv1.RequestContext, resourceID string) (*ragv1.Fact, error) {
	if err := ValidateNames([]string{tool}); err != nil {
		return nil, err
	}
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	if requiresResourceBinding(tool) && strings.TrimSpace(resourceID) == "" {
		return nil, ErrUnauthorized
	}
	base := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if base == "" {
		return nil, ErrUnavailable
	}
	version := strings.TrimSpace(p.Version)
	if version == "" {
		version = "v1"
	}
	u, err := url.Parse(base + "/v1/facts/" + url.PathEscape(tool))
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, ErrUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Facts-Version", version)
	request.Header.Set("X-Tenant-ID", scope.TenantId)
	request.Header.Set("X-User-ID", scope.UserId)
	request.Header.Set("X-Shop-ID", scope.ShopId)
	if requiresResourceBinding(tool) {
		request.Header.Set("X-Fact-Resource-ID", resourceID)
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, ErrCanceled
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrTimedOut
		}
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, ErrUnauthorized
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimited
	}
	if response.StatusCode >= 500 {
		return nil, ErrUnavailable
	}
	if response.StatusCode != http.StatusOK {
		return nil, ErrInvalidResponse
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil || len(body) > 256*1024 {
		return nil, ErrInvalidResponse
	}
	var payload httpFactResponse
	if json.Unmarshal(body, &payload) != nil || payload.Version != version || payload.TenantID != scope.TenantId || payload.ShopID != scope.ShopId || payload.ToolName != tool || len(payload.ValueJSON) == 0 || !json.Valid(payload.ValueJSON) || string(payload.ValueJSON) == "null" {
		return nil, ErrInvalidResponse
	}
	if !requiresResourceBinding(tool) && payload.ResourceID != "" {
		return nil, ErrInvalidResponse
	}
	if requiresResourceBinding(tool) && payload.ResourceID != resourceID {
		return nil, ErrInvalidResponse
	}
	if requiresResourceBinding(tool) {
		var value map[string]any
		if json.Unmarshal(payload.ValueJSON, &value) != nil || value[resourceField(tool)] != resourceID {
			return nil, ErrInvalidResponse
		}
	}
	if _, err := time.Parse(time.RFC3339, payload.Observed); err != nil {
		return nil, ErrInvalidResponse
	}
	return &ragv1.Fact{ToolName: tool, ValueJson: string(payload.ValueJSON), ObservedAt: payload.Observed, IsMock: payload.IsMock, ResourceId: payload.ResourceID}, nil
}

func validateScope(scope *ragv1.RequestContext) error {
	if scope == nil || scope.TenantId == "" || scope.UserId == "" || scope.ShopId == "" {
		return ErrUnauthorized
	}
	for _, shop := range scope.AllowedShopIds {
		if shop == scope.ShopId {
			return nil
		}
	}
	return fmt.Errorf("%w: shop scope", ErrUnauthorized)
}
