package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
)

const (
	gapsDefaultMinCount = 1
	gapExportDir        = ".local/gap-exports"
)

// adminGaps returns the content-gap list for the caller's tenant. The tenant
// comes from the session; a client-supplied tenant is never read.
func (g *Handler) adminGaps(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleObserver, adminRoleAccess, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	days, valid := outcomesRangeDays(r.URL.Query().Get("range"))
	if !valid {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_range", "range must be 7d or 30d")
		return
	}
	minCount, valid := gapMinCount(r.URL.Query().Get("min_count"))
	if !valid {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_min_count", "min_count must be a positive integer")
		return
	}
	from := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	items, truncated, err := adminStore.KnowledgeGaps(r.Context(), g.DB, scope.TenantID, from, minCount)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "gaps_unavailable", "gaps unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "truncated": truncated})
}

// adminGapsExport writes the current gap list as evaluation-case JSONL under
// .local/gap-exports. It never writes the evaluation set or documentation.
func (g *Handler) adminGapsExport(w http.ResponseWriter, r *http.Request) {
	scope, ok := g.requireAdmin(w, r, adminRoleObserver, adminRoleAccess, adminRoleMerchant, adminRoleReviewer)
	if !ok {
		return
	}
	days, valid := outcomesRangeDays(r.URL.Query().Get("range"))
	if !valid {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_range", "range must be 7d or 30d")
		return
	}
	from := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	items, _, err := adminStore.KnowledgeGaps(r.Context(), g.DB, scope.TenantID, from, gapsDefaultMinCount)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "gaps_unavailable", "gaps unavailable")
		return
	}
	path, count, err := writeGapExport(scope.TenantID, items)
	if err != nil {
		log.Printf("gap export failed: %v", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "gap_export_failed", "gap export failed")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"path": path, "count": count})
}

// gapMinCount accepts the documented default and any positive integer.
func gapMinCount(value string) (int64, bool) {
	if value == "" {
		return gapsDefaultMinCount, true
	}
	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil || count < 1 {
		return 0, false
	}
	return count, true
}

// writeGapExport creates a private JSONL file whose lines can be copied into
// the frozen evaluation corpus by a human. It never mutates the corpus.
func writeGapExport(tenantID int64, items []KnowledgeGap) (string, int, error) {
	dir := gapExportRoot()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	name := fmt.Sprintf("gaps-%d-%s.jsonl", tenantID, time.Now().UTC().Format("20060102T150405.000000000Z"))
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	capturedAt := time.Now().UTC().Format(time.RFC3339)
	for _, item := range items {
		record := struct {
			Query       string `json:"query"`
			Expectation string `json:"expectation"`
			Source      string `json:"source"`
			CapturedAt  string `json:"captured_at"`
		}{item.Query, "answer_or_clarify", "gap-" + item.Reason, capturedAt}
		if err := encoder.Encode(record); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return "", 0, err
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", 0, err
	}
	return filepath.ToSlash(path), len(items), nil
}

// gapExportRoot anchors exports at the repository root even when the service
// runs with a subdirectory as its working directory. ECR_GAP_EXPORT_DIR is the
// explicit override for tests and non-repository deployments.
func gapExportRoot() string {
	if dir := os.Getenv("ECR_GAP_EXPORT_DIR"); dir != "" {
		return dir
	}
	if dir, err := os.Getwd(); err == nil {
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return filepath.Join(dir, ".local", "gap-exports")
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return gapExportDir
}
