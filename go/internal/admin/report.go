package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/ggb5023/EcommerceRAG/go/internal/httpapi"
)

// The report page renders the same JSON the report CLI wrote. It never
// recomputes a metric. Only these five top-level sections are exposed; a future
// CLI field cannot leak through this endpoint without a contract change.
const maxReportBytes = 1 << 20

var reportSections = []string{"input", "operations", "handoff", "quality", "tokens"}

var errReportUnavailable = errors.New("evaluation report is unavailable")

// adminReport serves the read-only evaluation report for merchant roles.
func (g *Handler) adminReport(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.requireAdmin(w, r, adminRoleMerchant, adminRoleReviewer); !ok {
		return
	}
	report, err := readReport(os.Getenv("ECR_REPORT_PATH"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "report_unavailable", "evaluation report is unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, report)
}

// readReport opens the configured file defensively: it must resolve to a
// regular file, fit the size bound, parse as a JSON object, and contain all
// five sections. Extra top-level keys are deliberately dropped.
func readReport(path string) (map[string]json.RawMessage, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errReportUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errReportUnavailable
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxReportBytes {
		return nil, errReportUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxReportBytes+1))
	if err != nil || len(raw) > maxReportBytes {
		return nil, errReportUnavailable
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed == nil {
		return nil, errReportUnavailable
	}
	report := make(map[string]json.RawMessage, len(reportSections))
	for _, section := range reportSections {
		value, ok := parsed[section]
		if !ok {
			return nil, errReportUnavailable
		}
		report[section] = value
	}
	return report, nil
}
