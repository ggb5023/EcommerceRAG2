package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImportBatch20FixtureHitsAllFourRules(t *testing.T) {
	root := filepath.Join("..", "..", "..", "tests", "fixtures", "import-batch-20")
	manifestBytes, err := os.ReadFile(filepath.Join(root, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var files []UploadedFile
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.CreateTemp(t.TempDir(), "fixture-*")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(content); err != nil {
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		files = append(files, UploadedFile{TempPath: file.Name(), DeclaredName: entry.Name(), DeclaredSize: int64(len(content))})
	}
	documents, err := parseManifest(string(manifestBytes), files)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 20 {
		t.Fatalf("documents=%d", len(documents))
	}
	existing := map[string]bool{documents[2].SourceHash: true}
	issues := precheckDocuments(documents, time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), existing)
	found := map[string]int{}
	for _, row := range issues {
		for _, issue := range row {
			found[issue.IssueCode]++
		}
	}
	for _, code := range []string{"missing_source", "expired_content", "duplicate_import", "missing_disclosure_basis"} {
		if found[code] != 1 {
			t.Fatalf("%s=%d, issues=%#v", code, found[code], found)
		}
	}
}
