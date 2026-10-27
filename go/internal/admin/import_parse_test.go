package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stagedFile(t *testing.T, name, content string) UploadedFile {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "upload-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(content))
	return UploadedFile{TempPath: file.Name(), DeclaredName: name, DeclaredSize: int64(len(content)),
		DeclaredSHA256: hex.EncodeToString(hash[:])}
}

func manifestWith(entries string) string {
	return "schema_version: 1\nsource:\n  id: docs\n  name: Docs\npipeline_version: v2-test\ndocuments:\n" + entries
}

func docEntry(id, title, path, format, hash string) string {
	return fmt.Sprintf("  - document_id: %s\n    title: %s\n    path: %s\n    format: %s\n    sha256: %s\n    disclosure_class: external_allowed\n    effective_from: 2026-01-01\n    effective_to: null\n", id, title, path, format, hash)
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func TestParseManifestMatchesPreSplitManualResult(t *testing.T) {
	firstContent := "# One\n\nFirst body.\n"
	secondContent := "# Two\n\nSecond body.\n"
	first := stagedFile(t, "one.md", firstContent)
	second := stagedFile(t, "two.md", secondContent)
	manifest := manifestWith(
		docEntry("doc-one", "Doc One", "one.md", "markdown", hashText(firstContent)) +
			docEntry("doc-two", "Doc Two", "two.md", "markdown", hashText(secondContent)))
	documents, err := parseManifest(manifest, []UploadedFile{first, second})
	if err != nil {
		t.Fatal(err)
	}
	manifestHash := hashText(manifest)
	expectedVersion := func(sourceHash string) string {
		sum := sha256.Sum256([]byte(sourceHash + "\x00v2-test\x00" + manifestHash))
		return "v-" + hex.EncodeToString(sum[:])[:20]
	}
	expected := []ParsedDocument{
		{DocumentID: "doc-one", Title: "Doc One", Format: "markdown", Path: "one.md",
			SourceHash: hashText(firstContent), DisclosureClass: "external_allowed",
			EffectiveFrom: "2026-01-01", VersionID: expectedVersion(hashText(firstContent)), TempPath: first.TempPath},
		{DocumentID: "doc-two", Title: "Doc Two", Format: "markdown", Path: "two.md",
			SourceHash: hashText(secondContent), DisclosureClass: "external_allowed",
			EffectiveFrom: "2026-01-01", VersionID: expectedVersion(hashText(secondContent)), TempPath: second.TempPath},
	}
	if len(documents) != len(expected) {
		t.Fatalf("documents=%d want %d", len(documents), len(expected))
	}
	for index := range expected {
		if documents[index] != expected[index] {
			t.Fatalf("document %d differs:\n got %#v\nwant %#v", index, documents[index], expected[index])
		}
	}
}

func TestParseManifestRejectsInvalidPackages(t *testing.T) {
	content := "# One\n\nBody.\n"
	file := stagedFile(t, "one.md", content)
	hash := hashText(content)
	tests := []struct {
		name     string
		manifest string
		files    []UploadedFile
	}{
		{"schema", strings.Replace(manifestWith(docEntry("doc", "Doc", "one.md", "markdown", hash)),
			"schema_version: 1", "schema_version: 2", 1), []UploadedFile{file}},
		{"missing_file", manifestWith(docEntry("doc", "Doc", "missing.md", "markdown", hash)), []UploadedFile{file}},
		{"hash_mismatch", manifestWith(docEntry("doc", "Doc", "one.md", "markdown", strings.Repeat("0", 64))), []UploadedFile{file}},
		{"path_traversal", manifestWith(docEntry("doc", "Doc", "../evil.md", "markdown", hash)),
			[]UploadedFile{{TempPath: file.TempPath, DeclaredName: "../evil.md", DeclaredSize: file.DeclaredSize}}},
		{"duplicate_path", manifestWith(docEntry("doc-a", "A", "one.md", "markdown", hash) + docEntry("doc-b", "B", "one.md", "markdown", hash)), []UploadedFile{file}},
		{"duplicate_file", manifestWith(docEntry("doc", "Doc", "one.md", "markdown", hash)), []UploadedFile{file, file}},
		{"unreferenced", manifestWith(docEntry("doc", "Doc", "one.md", "markdown", hash)), []UploadedFile{file, stagedFile(t, "extra.md", "extra")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseManifest(test.manifest, test.files); err == nil {
				t.Fatal("invalid package accepted")
			}
		})
	}
}

func TestParseManifestStreamsLargeFile(t *testing.T) {
	content := strings.Repeat("a", 2*1024*1024)
	file := stagedFile(t, "large.md", content)
	manifest := manifestWith(docEntry("doc", "Large", "large.md", "markdown", hashText(content)))
	if _, err := parseManifest(manifest, []UploadedFile{file}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(file.TempPath); err != nil || info.Size() != int64(len(content)) {
		t.Fatalf("staged file changed: %v", err)
	}
	if filepath.Base(file.TempPath) == "" {
		t.Fatal("temporary path missing")
	}
}
