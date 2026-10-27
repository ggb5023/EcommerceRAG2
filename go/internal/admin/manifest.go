package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	maxManifestBytes  = 256 * 1024
	maxImportFileSize = 8 * 1024 * 1024
	maxImportFiles    = 100
)

var (
	manifestIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	manifestSHA256     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// UploadedFile is a streamed upload staged outside the business directories.
// parseManifest opens TempPath and never holds the whole document in memory.
type UploadedFile struct {
	TempPath       string
	DeclaredName   string
	DeclaredSize   int64
	DeclaredSHA256 string
}

type ParsedDocument struct {
	DocumentID             string
	Title                  string
	Format                 string
	Path                   string
	SourceHash             string
	DisclosureClass        string
	EffectiveFrom          string
	EffectiveTo            string
	VersionID              string
	TempPath               string
	License                string
	AuthorizationReference string
	CheckedAt              string
	Snippet                string
}

type ParsedPackage struct {
	Manifest  string
	Files     []UploadedFile
	Documents []ParsedDocument
}

type rawManifest struct {
	SchemaVersion   any              `yaml:"schema_version"`
	Source          map[string]any   `yaml:"source"`
	PipelineVersion any              `yaml:"pipeline_version"`
	License         any              `yaml:"license"`
	Documents       []map[string]any `yaml:"documents"`
}

func manifestString(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func manifestDate(value any) (string, bool) {
	if value == nil {
		return "", true
	}
	if text, ok := value.(string); ok {
		parsed, err := time.Parse("2006-01-02", text)
		if err != nil {
			return "", false
		}
		return parsed.Format("2006-01-02"), true
	}
	if stamp, ok := value.(time.Time); ok {
		return stamp.Format("2006-01-02"), true
	}
	return "", false
}

func safeManifestPath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return false
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "." {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func hashUploadedFile(file UploadedFile) (string, int64, error) {
	stream, err := os.Open(file.TempPath)
	if err != nil {
		return "", 0, err
	}
	defer stream.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(stream, maxImportFileSize+1))
	if err != nil {
		return "", 0, err
	}
	if size > maxImportFileSize {
		return "", size, fmt.Errorf("file exceeds limit")
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

// parseManifest validates the manifest schema, paths and streamed file hashes.
// It is pure with respect to business state: no database, RPC or business writes.
func parseManifest(manifest string, files []UploadedFile) ([]ParsedDocument, error) {
	if len(manifest) == 0 || len(manifest) > maxManifestBytes {
		return nil, fmt.Errorf("manifest size is outside the allowed limit")
	}
	if len(files) < 1 || len(files) > maxImportFiles {
		return nil, fmt.Errorf("package file count is outside the allowed limit")
	}
	uploaded := make(map[string]UploadedFile, len(files))
	var total int64
	for _, file := range files {
		if !safeManifestPath(file.DeclaredName) {
			return nil, fmt.Errorf("package contains an unsafe file path")
		}
		if _, exists := uploaded[file.DeclaredName]; exists {
			return nil, fmt.Errorf("package contains a duplicate file path")
		}
		total += file.DeclaredSize
		if total > maxImportFileSize {
			return nil, fmt.Errorf("package size exceeds the allowed limit")
		}
		uploaded[file.DeclaredName] = file
	}
	var raw rawManifest
	if err := yaml.Unmarshal([]byte(manifest), &raw); err != nil {
		return nil, fmt.Errorf("manifest YAML is invalid")
	}
	schemaOK := false
	switch version := raw.SchemaVersion.(type) {
	case int:
		schemaOK = version == 1
	case float64:
		schemaOK = version == 1
	}
	if !schemaOK {
		return nil, fmt.Errorf("manifest schema_version must be 1")
	}
	sourceID, ok := manifestString(raw.Source["id"])
	if !ok || !manifestIdentifier.MatchString(sourceID) {
		return nil, fmt.Errorf("manifest source.id is invalid")
	}
	sourceName, ok := manifestString(raw.Source["name"])
	if !ok || strings.TrimSpace(sourceName) == "" || len(sourceName) > 200 {
		return nil, fmt.Errorf("manifest source.name is invalid")
	}
	pipeline, ok := manifestString(raw.PipelineVersion)
	if !ok || !manifestIdentifier.MatchString(pipeline) {
		return nil, fmt.Errorf("manifest pipeline_version is invalid")
	}
	if len(raw.Documents) < 1 || len(raw.Documents) > maxImportFiles {
		return nil, fmt.Errorf("manifest documents count is outside the allowed limit")
	}
	manifestDigest := sha256.Sum256([]byte(manifest))
	manifestHash := hex.EncodeToString(manifestDigest[:])
	usedPaths := make(map[string]bool, len(raw.Documents))
	documentIDs := make(map[string]bool, len(raw.Documents))
	var actualTotal int64
	parsed := make([]ParsedDocument, 0, len(raw.Documents))
	for index, spec := range raw.Documents {
		documentID, ok := manifestString(spec["document_id"])
		if !ok || !manifestIdentifier.MatchString(documentID) || documentIDs[documentID] {
			return nil, fmt.Errorf("documents[%d].document_id is invalid or duplicated", index)
		}
		documentIDs[documentID] = true
		title, ok := manifestString(spec["title"])
		if !ok || strings.TrimSpace(title) == "" || len(title) > 300 {
			return nil, fmt.Errorf("documents[%d].title is invalid", index)
		}
		relative, ok := manifestString(spec["path"])
		if !ok || !safeManifestPath(relative) {
			return nil, fmt.Errorf("documents[%d].path is unsafe", index)
		}
		format, ok := manifestString(spec["format"])
		if !ok || (format != "markdown" && format != "csv" && format != "docx") {
			return nil, fmt.Errorf("documents[%d].format is unsupported", index)
		}
		extension := strings.ToLower(path.Ext(relative))
		extensionOK := (format == "markdown" && (extension == ".md" || extension == ".markdown")) ||
			(format == "csv" && extension == ".csv") || (format == "docx" && extension == ".docx")
		if !extensionOK {
			return nil, fmt.Errorf("documents[%d] format does not match its file", index)
		}
		disclosure := "unclassified"
		ok = true
		if declaredDisclosure, present := spec["disclosure_class"]; present {
			disclosure, ok = manifestString(declaredDisclosure)
		}
		if !ok || (disclosure != "external_allowed" && disclosure != "internal_only" && disclosure != "unclassified") {
			return nil, fmt.Errorf("documents[%d].disclosure_class is invalid", index)
		}
		effectiveFrom, ok := manifestDate(spec["effective_from"])
		if !ok {
			return nil, fmt.Errorf("documents[%d].effective_from must be an ISO date", index)
		}
		effectiveTo, ok := manifestDate(spec["effective_to"])
		if !ok {
			return nil, fmt.Errorf("documents[%d].effective_to must be an ISO date", index)
		}
		if effectiveFrom != "" && effectiveTo != "" && effectiveTo <= effectiveFrom {
			return nil, fmt.Errorf("documents[%d] effective_to must be after effective_from", index)
		}
		if usedPaths[relative] {
			return nil, fmt.Errorf("documents[%d].path is missing or duplicated", index)
		}
		usedPaths[relative] = true
		file, exists := uploaded[relative]
		if !exists {
			return nil, fmt.Errorf("documents[%d].path is missing or duplicated", index)
		}
		if file.DeclaredSHA256 != "" && !manifestSHA256.MatchString(file.DeclaredSHA256) {
			return nil, fmt.Errorf("documents[%d].sha256 is invalid", index)
		}
		declared, ok := manifestString(spec["sha256"])
		if !ok || !manifestSHA256.MatchString(declared) {
			return nil, fmt.Errorf("documents[%d].sha256 is invalid", index)
		}
		sourceHash, size, err := hashUploadedFile(file)
		if err != nil {
			return nil, err
		}
		actualTotal += size
		if actualTotal > maxImportFileSize {
			return nil, fmt.Errorf("package size exceeds the allowed limit")
		}
		if file.DeclaredSize >= 0 && file.DeclaredSize != size {
			return nil, fmt.Errorf("documents[%d].size does not match the uploaded file", index)
		}
		if declared != sourceHash || (file.DeclaredSHA256 != "" && file.DeclaredSHA256 != sourceHash) {
			return nil, fmt.Errorf("documents[%d].sha256 does not match the uploaded file", index)
		}
		versionDigest := sha256.Sum256([]byte(sourceHash + "\x00" + pipeline + "\x00" + manifestHash))
		license, _ := manifestString(raw.License)
		if declaredLicense, present := spec["license"]; present {
			license, _ = manifestString(declaredLicense)
		}
		authorizationReference, checkedAt := "", ""
		if authorization, ok := spec["authorization"].(map[string]any); ok {
			authorizationReference, _ = manifestString(authorization["reference"])
			checkedAt, _ = manifestString(authorization["checked_at"])
		}
		snippet, _ := manifestString(spec["excerpt"])
		parsed = append(parsed, ParsedDocument{
			DocumentID: documentID, Title: strings.TrimSpace(title), Format: format, Path: relative,
			SourceHash: sourceHash, DisclosureClass: disclosure, EffectiveFrom: effectiveFrom,
			EffectiveTo: effectiveTo, VersionID: "v-" + hex.EncodeToString(versionDigest[:])[:20],
			TempPath: file.TempPath, License: license, AuthorizationReference: authorizationReference,
			CheckedAt: checkedAt, Snippet: snippet,
		})
	}
	if len(usedPaths) != len(uploaded) {
		return nil, fmt.Errorf("package contains unreferenced files")
	}
	return parsed, nil
}
