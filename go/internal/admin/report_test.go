package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validReportJSON = `{
  "report_version": "demo-report-v1",
  "status": "NOT_MEASURED",
  "input": {"status": "MEASURED", "git_commit": "abc"},
  "operations": {"status": "MEASURED", "turns_total": 0, "auto_rate": null},
  "handoff": {"status": "MEASURED", "handoff_conversations": 0, "customer_conversations": 0, "handoff_rate": null},
  "quality": {"status": "NOT_MEASURED", "wrong_answer_rate": null},
  "tokens": {"status": "MEASURED", "input_tokens": 0, "input_tokens_per_1000": null},
  "leak": {"secret": "must not pass"},
  "not_measured": ["quality"]
}`

func writeReportFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAdminReportWhitelistsSections(t *testing.T) {
	path := writeReportFile(t, validReportJSON)
	report, err := readReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(report) != 5 {
		t.Fatalf("sections=%d want 5: %v", len(report), report)
	}
	for _, section := range reportSections {
		if _, ok := report[section]; !ok {
			t.Fatalf("missing section %q", section)
		}
	}
	if _, ok := report["leak"]; ok {
		t.Fatal("extra top-level key was not dropped")
	}
	if _, ok := report["not_measured"]; ok {
		t.Fatal("CLI metadata was not dropped")
	}
	if !bytes.Contains(report["quality"], []byte(`"NOT_MEASURED"`)) {
		t.Fatalf("quality section was altered: %s", report["quality"])
	}

	t.Run("missing section", func(t *testing.T) {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(validReportJSON), &parsed); err != nil {
			t.Fatal(err)
		}
		delete(parsed, "tokens")
		raw, _ := json.Marshal(parsed)
		if _, err := readReport(writeReportFile(t, string(raw))); err == nil {
			t.Fatal("a report without all five sections was accepted")
		}
	})
}

func TestReportHandlesUnavailableFiles(t *testing.T) {
	dir := t.TempDir()
	large := filepath.Join(dir, "large.json")
	if err := os.WriteFile(large, bytes.Repeat([]byte("a"), maxReportBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string
	}{
		{name: "empty path", path: "  "},
		{name: "missing file", path: filepath.Join(dir, "missing.json")},
		{name: "directory", path: dir},
		{name: "too large", path: large},
		{name: "empty file", path: writeReportFile(t, "")},
		{name: "bad json", path: writeReportFile(t, "{not-json")},
		{name: "array root", path: writeReportFile(t, "[]")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readReport(tc.path); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
		})
	}
}

func TestReportRequiresMerchantRole(t *testing.T) {
	handler := &Handler{}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	request := httptest.NewRequest("GET", "/admin/v1/report", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "admin_scope_denied") {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
}
