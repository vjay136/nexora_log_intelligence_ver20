package controlplane

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"nexora.local/nli-v20/internal/middleware"
	"nexora.local/nli-v20/internal/migration"
	metastore "nexora.local/nli-v20/internal/store"
	parserpolicy "nexora.local/nli-v20/parser-policy"
)

const (
	migrationMaxSourceBytes       = int64(2 << 20)
	migrationMaxArchiveBytes      = int64(4 << 20)
	migrationMaxExpandedBytes     = int64(8 << 20)
	migrationMaxRequestBytes      = int64(7 << 20)
	migrationMaxCandidateBytes    = 1 << 20
	migrationMaxStoredPayload     = 3 << 20
	migrationMaxArchiveFiles      = 32
	migrationMaxCompressionRatio  = uint64(200)
	migrationCompressionThreshold = uint64(1 << 20)
)

type migrationImportRequest struct {
	Filename            string                        `json:"filename"`
	SourceType          string                        `json:"source_type,omitempty"`
	Content             string                        `json:"content,omitempty"`
	ContentBase64       string                        `json:"content_base64,omitempty"`
	SHA256              string                        `json:"sha256,omitempty"`
	CreateDraft         bool                          `json:"create_draft"`
	RegressionCandidate *migrationRegressionCandidate `json:"regression_candidate,omitempty"`
}

type migrationRegressionCandidate struct {
	Name           string         `json:"name,omitempty"`
	Raw            string         `json:"raw"`
	Expected       map[string]any `json:"expected"`
	Service        string         `json:"service,omitempty"`
	SourceType     string         `json:"source_type,omitempty"`
	ExpectedParser string         `json:"expected_parser,omitempty"`
	ExpectedRoute  string         `json:"expected_route,omitempty"`
}

type migrationCandidateAssessment struct {
	Provided       bool                          `json:"provided"`
	Name           string                        `json:"name"`
	InputSHA256    string                        `json:"input_sha256"`
	ExpectedSHA256 string                        `json:"expected_sha256"`
	ParityStatus   string                        `json:"parity_status"`
	Outcomes       []migration.RegressionOutcome `json:"outcomes"`
	Retained       bool                          `json:"retained"`
	NextStep       string                        `json:"next_step"`
}

type migrationArchiveEntry struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	MediaType  string `json:"media_type"`
	SourceType string `json:"source_type"`
	Status     string `json:"status"`
	content    []byte
}

type migrationGeneratedPack struct {
	SourceFile              string `json:"source_file"`
	ID                      string `json:"id"`
	Version                 string `json:"version"`
	Revision                int    `json:"revision"`
	SHA256                  string `json:"sha256"`
	SourceHash              string `json:"source_hash"`
	RuleCount               int    `json:"rule_count"`
	ReviewState             string `json:"review_state"`
	DraftID                 string `json:"draft_id,omitempty"`
	Activated               bool   `json:"activated"`
	RegressionCasesRetained bool   `json:"regression_cases_retained"`
}

type migrationSupportedConstruct struct {
	SourceFile string `json:"source_file"`
	Construct  string `json:"construct"`
	Line       int    `json:"line"`
	IRRuleID   string `json:"ir_rule_id,omitempty"`
	IRStageID  string `json:"ir_stage_id,omitempty"`
}

type migrationUnsupportedConstruct struct {
	SourceFile  string                `json:"source_file"`
	Type        string                `json:"type"`
	Description string                `json:"description"`
	Range       migration.SourceRange `json:"range"`
	Severity    string                `json:"severity"`
	Repair      string                `json:"repair,omitempty"`
}

type migrationResultView struct {
	SourceFile            string                           `json:"source_file"`
	SourceFormat          string                           `json:"source_format"`
	SourceSHA256          string                           `json:"source_sha256"`
	Parsed                bool                             `json:"parsed"`
	Success               bool                             `json:"success"`
	GeneratedPack         *migrationGeneratedPack          `json:"generated_pack,omitempty"`
	Operations            []migration.OperationRecord      `json:"operations"`
	LineMappings          []migration.LineMapping          `json:"line_mappings"`
	UnsupportedConstructs []migration.UnsupportedConstruct `json:"unsupported_constructs"`
	RuleAssessments       []migration.RuleAssessment       `json:"rule_assessments"`
	RegressionOutcomes    []migration.RegressionOutcome    `json:"regression_outcomes,omitempty"`
	ParityStatus          string                           `json:"parity_status"`
	Warnings              []string                         `json:"warnings,omitempty"`
	Errors                []string                         `json:"errors,omitempty"`
}

type migrationCompatibility struct {
	SourceFiles       int            `json:"source_files"`
	ParsedFiles       int            `json:"parsed_files"`
	TranslatedFiles   int            `json:"translated_files"`
	VerifiedFiles     int            `json:"verified_files"`
	UnverifiedFiles   int            `json:"unverified_files"`
	FailedFiles       int            `json:"failed_files"`
	OperationsByKind  map[string]int `json:"operations_by_kind"`
	TranslatedByKind  map[string]int `json:"translated_by_kind"`
	UnsupportedByKind map[string]int `json:"unsupported_by_kind"`
	RequiredRepairs   []string       `json:"required_repairs,omitempty"`
}

type migrationImportReport struct {
	SchemaVersion          string                          `json:"schema_version"`
	Filename               string                          `json:"filename"`
	SourceType             string                          `json:"source_type"`
	SourceHash             string                          `json:"source_hash"`
	SourceSize             int64                           `json:"source_size"`
	SourceMediaType        string                          `json:"source_media_type"`
	ReportHash             string                          `json:"report_hash,omitempty"`
	SourceRetained         bool                            `json:"source_retained"`
	RawPayloadRetained     bool                            `json:"raw_payload_retained"`
	ManualApprovalRequired bool                            `json:"manual_approval_required"`
	AutoActivated          bool                            `json:"auto_activated"`
	ParityStatus           string                          `json:"parity_status"`
	SupportedCount         int                             `json:"supported_count"`
	UnsupportedCount       int                             `json:"unsupported_count"`
	Supported              []migrationSupportedConstruct   `json:"supported"`
	Unsupported            []migrationUnsupportedConstruct `json:"unsupported"`
	Compatibility          migrationCompatibility          `json:"compatibility"`
	Results                []migrationResultView           `json:"results"`
	GeneratedPacks         []migrationGeneratedPack        `json:"generated_packs"`
	DraftID                string                          `json:"draft_id,omitempty"`
	DraftIDs               []string                        `json:"draft_ids,omitempty"`
	ArchiveEntries         []migrationArchiveEntry         `json:"archive_entries,omitempty"`
	CandidateAssessment    *migrationCandidateAssessment   `json:"candidate_assessment,omitempty"`
}

type preparedMigrationDraft struct {
	ID      string
	Payload json.RawMessage
}

func (s *Server) handleMigrationImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	principal := middleware.GetSecurityContext(r)
	if principal == nil {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var request migrationImportRequest
	if err := decodeJSON(r, &request, migrationMaxRequestBytes); err != nil {
		jsonError(w, "invalid bounded migration request", http.StatusBadRequest)
		return
	}
	filename, err := safeFilename(request.Filename)
	if err != nil {
		jsonError(w, "a safe filename is required", http.StatusBadRequest)
		return
	}
	content, err := decodeMigrationContent(request)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	kind, err := migrationSourceKind(request.SourceType, filename, content)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	limit := migrationMaxSourceBytes
	if kind == "zip" {
		limit = migrationMaxArchiveBytes
	}
	if int64(len(content)) > limit {
		jsonError(w, fmt.Sprintf("migration upload exceeds %d bytes", limit), http.StatusRequestEntityTooLarge)
		return
	}
	if kind != "zip" && bytes.IndexByte(content, 0) >= 0 {
		jsonError(w, "migration source must be text", http.StatusBadRequest)
		return
	}
	digest := sha256.Sum256(content)
	sourceHash := hex.EncodeToString(digest[:])
	if request.SHA256 != "" && !strings.EqualFold(strings.TrimSpace(request.SHA256), sourceHash) {
		jsonError(w, "migration source SHA-256 does not match", http.StatusBadRequest)
		return
	}

	regression, assessment, err := prepareMigrationCandidate(request.RegressionCandidate)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if kind == "zip" && regression != nil {
		jsonError(w, "a regression candidate must target a single non-archive source", http.StatusBadRequest)
		return
	}

	results, archiveEntries, err := runMigrationPipeline(r.Context(), kind, filename, content, regression)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	drafts, generated, err := prepareMigrationDrafts(results, request.CreateDraft)
	if err != nil {
		jsonError(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if assessment != nil {
		completeCandidateAssessment(assessment, results)
	}
	report := buildMigrationImportReport(filename, kind, content, sourceHash, results, archiveEntries, generated, assessment)
	payload, err := sealMigrationReport(&report)
	if err != nil {
		jsonError(w, "migration report could not be encoded", http.StatusInternalServerError)
		return
	}
	if len(payload) > migrationMaxStoredPayload {
		jsonError(w, "bounded compatibility metadata exceeds the persistence limit", http.StatusRequestEntityTooLarge)
		return
	}

	for _, draft := range drafts {
		if _, err = s.metadata.Put(r.Context(), metastore.Document{Collection: "parser-packs", ID: draft.ID, State: "draft", Payload: draft.Payload, CreatedBy: principal.Username}); err != nil {
			jsonError(w, "draft parser pack persistence failed", http.StatusInternalServerError)
			return
		}
	}
	id := "migration-" + report.ReportHash[:24]
	saved, err := s.metadata.Put(r.Context(), metastore.Document{Collection: "migration-reports", ID: id, State: "analyzed", Payload: payload, CreatedBy: principal.Username})
	if err != nil {
		jsonError(w, "migration report persistence failed", http.StatusInternalServerError)
		return
	}
	s.auditAction(principal, "migration.analyze", "migration-reports/"+id, "success", map[string]string{
		"source_hash": sourceHash,
		"report_hash": report.ReportHash,
		"source_type": kind,
		"draft_count": fmt.Sprint(len(drafts)),
	})
	jsonCreated(w, documentView(saved))
}

func decodeMigrationContent(request migrationImportRequest) ([]byte, error) {
	if request.Content != "" && request.ContentBase64 != "" {
		return nil, fmt.Errorf("provide exactly one of content or content_base64")
	}
	if request.Content == "" && request.ContentBase64 == "" {
		return nil, fmt.Errorf("parser source content is required")
	}
	if request.ContentBase64 == "" {
		return []byte(request.Content), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(request.ContentBase64)
	if err != nil {
		return nil, fmt.Errorf("content_base64 must be standard base64")
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("parser source content is required")
	}
	return decoded, nil
}

func migrationSourceKind(explicit, filename string, content []byte) (string, error) {
	normalize := func(value string) string {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "alloy", "river", "hcl":
			return "alloy"
		case "vector", "toml", "vrl":
			return "vector"
		case "nli", "parserpack", "parser-pack":
			return "nli"
		case "zip", "archive":
			return "zip"
		default:
			return ""
		}
	}
	kind := normalize(explicit)
	if explicit != "" && kind == "" {
		return "", fmt.Errorf("supported source types are alloy, vector, nli, and zip")
	}
	extKind := ""
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".alloy", ".river", ".hcl":
		extKind = "alloy"
	case ".toml", ".vrl":
		extKind = "vector"
	case ".json":
		extKind = "nli"
	case ".zip":
		extKind = "zip"
	}
	if kind != "" && extKind != "" && kind != extKind {
		return "", fmt.Errorf("source type %s does not match filename extension", kind)
	}
	if kind == "" {
		kind = extKind
	}
	if kind == "" {
		text := string(content)
		switch {
		case bytes.HasPrefix(content, []byte("PK\x03\x04")):
			kind = "zip"
		case strings.Contains(text, "loki.process") || strings.Contains(text, "loki.source.file"):
			kind = "alloy"
		case strings.Contains(text, "[transforms.") || strings.Contains(text, "[sources."):
			kind = "vector"
		case json.Valid(content):
			kind = "nli"
		}
	}
	if kind == "" {
		return "", fmt.Errorf("could not identify a supported migration source type")
	}
	return kind, nil
}

func prepareMigrationCandidate(input *migrationRegressionCandidate) (*parserpolicy.RegressionCase, *migrationCandidateAssessment, error) {
	if input == nil {
		return nil, nil, nil
	}
	if input.Raw == "" || len(input.Raw) > migrationMaxCandidateBytes {
		return nil, nil, fmt.Errorf("regression candidate raw input must be 1-%d bytes", migrationMaxCandidateBytes)
	}
	if len(input.Expected) == 0 || len(input.Expected) > 1000 {
		return nil, nil, fmt.Errorf("regression candidate expected payload must contain 1-1000 fields")
	}
	expected, err := json.Marshal(input.Expected)
	if err != nil || len(expected) > migrationMaxCandidateBytes {
		return nil, nil, fmt.Errorf("regression candidate expected payload exceeds its bound")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "migration-candidate"
	}
	expectedParser := strings.TrimSpace(input.ExpectedParser)
	if expectedParser == "" {
		expectedParser = "*"
	}
	if len(name) > 128 || len(input.Service) > 64 || len(input.SourceType) > 64 || len(expectedParser) > 128 || len(input.ExpectedRoute) > 64 {
		return nil, nil, fmt.Errorf("regression candidate metadata exceeds parser-policy limits")
	}
	regression := &parserpolicy.RegressionCase{
		Name: name, Input: input.Raw, Service: input.Service, SourceType: input.SourceType,
		ExpectedPayload: input.Expected, ExpectedParser: expectedParser, ExpectedRoute: input.ExpectedRoute,
	}
	inputHash := sha256.Sum256([]byte(input.Raw))
	expectedHash := sha256.Sum256(expected)
	assessment := &migrationCandidateAssessment{
		Provided: true, Name: name, InputSHA256: hex.EncodeToString(inputHash[:]), ExpectedSHA256: hex.EncodeToString(expectedHash[:]),
		ParityStatus: migration.ParityUnverified, Outcomes: []migration.RegressionOutcome{}, Retained: false,
		NextStep: "submit the candidate to Assurance Lab for explicit encrypted retention, then obtain manual approval",
	}
	return regression, assessment, nil
}

func runMigrationPipeline(ctx context.Context, kind, filename string, content []byte, regression *parserpolicy.RegressionCase) ([]migration.MigrationResult, []migrationArchiveEntry, error) {
	regressions := []parserpolicy.RegressionCase{}
	if regression != nil {
		regressions = append(regressions, *regression)
	}
	switch kind {
	case "alloy":
		result, _ := migration.TranslateAlloy(content, filename, regressions...)
		if result == nil {
			return nil, nil, fmt.Errorf("Alloy/River migration failed")
		}
		return []migration.MigrationResult{*result}, nil, nil
	case "vector":
		result, _ := migration.TranslateVector(content, filename, regressions...)
		if result == nil {
			return nil, nil, fmt.Errorf("Vector migration failed")
		}
		return []migration.MigrationResult{*result}, nil, nil
	case "nli":
		result := importNLIParserPack(ctx, content, filename, regression)
		return []migration.MigrationResult{result}, nil, nil
	case "zip":
		entries, err := scanMigrationArchive(content)
		if err != nil {
			return nil, nil, err
		}
		results := []migration.MigrationResult{}
		hasTranslatedSource := false
		for _, entry := range entries {
			if entry.SourceType == "alloy" || entry.SourceType == "vector" {
				hasTranslatedSource = true
				break
			}
		}
		if hasTranslatedSource {
			processor := &migration.BatchProcessor{MaxFileBytes: migrationMaxSourceBytes, MaxFiles: migrationMaxArchiveFiles, MaxTotalBytes: migrationMaxExpandedBytes}
			batch, processErr := processor.ProcessArchive(content)
			if processErr != nil {
				return nil, nil, processErr
			}
			results = append(results, batch.Results...)
		}
		for _, entry := range entries {
			if entry.SourceType == "nli" {
				results = append(results, importNLIParserPack(ctx, entry.content, entry.Name, nil))
			}
		}
		sort.SliceStable(results, func(i, j int) bool { return results[i].SourceFile < results[j].SourceFile })
		return results, entries, nil
	default:
		return nil, nil, fmt.Errorf("unsupported migration source type")
	}
}

func scanMigrationArchive(data []byte) ([]migrationArchiveEntry, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid migration ZIP archive")
	}
	if len(reader.File) > migrationMaxArchiveFiles {
		return nil, fmt.Errorf("migration archive has %d entries; maximum is %d", len(reader.File), migrationMaxArchiveFiles)
	}
	entries := make([]migrationArchiveEntry, 0, len(reader.File))
	seen := map[string]struct{}{}
	var expanded int64
	for _, file := range reader.File {
		if file.Flags&0x1 != 0 {
			return nil, fmt.Errorf("migration archive contains encrypted entry %q", file.Name)
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if file.Mode()&os.ModeType != 0 {
			return nil, fmt.Errorf("migration archive contains non-regular entry %q", file.Name)
		}
		name, nameErr := safeMigrationArchiveName(file.Name)
		if nameErr != nil {
			return nil, nameErr
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("migration archive contains duplicate entry %q", name)
		}
		seen[name] = struct{}{}
		if file.UncompressedSize64 > uint64(migrationMaxSourceBytes) {
			return nil, fmt.Errorf("archive entry %q exceeds %d bytes", name, migrationMaxSourceBytes)
		}
		if file.UncompressedSize64 > migrationCompressionThreshold && (file.CompressedSize64 == 0 || file.CompressedSize64 < file.UncompressedSize64/migrationMaxCompressionRatio) {
			return nil, fmt.Errorf("archive entry %q exceeds the maximum compression ratio", name)
		}
		stream, openErr := file.Open()
		if openErr != nil {
			return nil, fmt.Errorf("archive entry %q cannot be read", name)
		}
		content, readErr := io.ReadAll(io.LimitReader(stream, migrationMaxSourceBytes+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || int64(len(content)) > migrationMaxSourceBytes || uint64(len(content)) != file.UncompressedSize64 {
			return nil, fmt.Errorf("archive entry %q failed bounded integrity validation", name)
		}
		expanded += int64(len(content))
		if expanded > migrationMaxExpandedBytes {
			return nil, fmt.Errorf("migration archive expands beyond %d bytes", migrationMaxExpandedBytes)
		}
		digest := sha256.Sum256(content)
		entryKind, kindErr := migrationSourceKind("", name, content)
		status := "supported"
		if kindErr != nil || entryKind == "zip" {
			entryKind = "unsupported"
			status = "unsupported"
		}
		mediaType := http.DetectContentType(content)
		entries = append(entries, migrationArchiveEntry{
			Name: name, Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), MediaType: mediaType,
			SourceType: entryKind, Status: status, content: content,
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("migration archive contains no regular files")
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func safeMigrationArchiveName(name string) (string, error) {
	if !utf8.ValidString(name) || strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("migration archive contains an invalid filename")
	}
	normalized := strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	cleaned := pathpkg.Clean(normalized)
	if normalized == "" || pathpkg.IsAbs(normalized) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") || strings.Contains(cleaned, ":") {
		return "", fmt.Errorf("unsafe migration archive filename %q", name)
	}
	return cleaned, nil
}

func importNLIParserPack(ctx context.Context, content []byte, filename string, regression *parserpolicy.RegressionCase) migration.MigrationResult {
	digest := sha256.Sum256(content)
	result := migration.MigrationResult{
		SourceFile: filename, SourceFormat: "nli", SourceSHA256: hex.EncodeToString(digest[:]),
		ParityStatus: migration.ParityUnverified,
	}
	pack, err := parserpolicy.LoadParserPackBytes(content)
	if err != nil {
		result.Errors = []string{"NLI ParserPack failed strict parser-policy validation"}
		return result
	}
	result.Parsed = true
	embeddedRegressions := len(pack.RegressionCases)
	pack.SourceType = "imported"
	pack.SourceHash = result.SourceSHA256
	pack.Author = "nli-migrate"
	pack.ReviewState = parserpolicy.ReviewStateDraft
	pack.ActivatedAt = nil
	pack.CreatedAt = time.Unix(0, 0).UTC()
	pack.Revision = 1
	pack.Metadata = map[string]string{"source_format": "nli-parser-pack"}
	pack.RegressionCases = nil
	if embeddedRegressions > 0 {
		result.Warnings = append(result.Warnings, "embedded regression cases were validated but were not executed or retained; submit samples through Assurance Lab")
	}
	if regression != nil {
		pack.RegressionCases = []parserpolicy.RegressionCase{*regression}
	}
	pack.SHA256 = ""
	if err = parserpolicy.SealParserPack(pack); err != nil {
		result.Errors = []string{"NLI ParserPack could not be normalized"}
		return result
	}
	validation := parserpolicy.ValidateParserPack(pack)
	if !validation.Valid {
		result.Errors = []string{"normalized NLI ParserPack failed parser-policy validation"}
		return result
	}
	fullRange := migrationFullSourceRange(content)
	result.Operations = append(result.Operations, migration.OperationRecord{Kind: "nli.parser_pack", Name: pack.ID, Range: fullRange, Status: "translated"})
	result.LineMappings = append(result.LineMappings, migration.LineMapping{SourceFile: filename, Construct: "nli.parser_pack", Range: fullRange, Status: "translated"})
	for _, rule := range pack.Rules {
		result.Operations = append(result.Operations, migration.OperationRecord{Kind: "nli.rule", Name: rule.ID, Range: fullRange, IRRuleID: rule.ID, Status: "translated"})
		result.LineMappings = append(result.LineMappings, migration.LineMapping{SourceFile: filename, Construct: "nli.rule", IRRuleID: rule.ID, Range: fullRange, Status: "translated"})
		result.RuleAssessments = append(result.RuleAssessments, migration.RuleAssessment{RuleID: rule.ID, Priority: rule.Priority, ParityStatus: migration.ParityUnverified})
		for _, stage := range rule.Stages {
			result.Operations = append(result.Operations, migration.OperationRecord{Kind: "nli.stage." + string(stage.Type), Name: stage.ID, Range: fullRange, IRRuleID: rule.ID, IRStageID: stage.ID, Status: "translated"})
			result.LineMappings = append(result.LineMappings, migration.LineMapping{SourceFile: filename, Construct: "nli.stage." + string(stage.Type), IRRuleID: rule.ID, IRStageID: stage.ID, Range: fullRange, Status: "translated"})
		}
	}
	result.ParserPack = pack
	result.Success = true
	if regression != nil {
		assessNLIRegression(ctx, &result, *regression)
	} else {
		result.Warnings = append(result.Warnings, "no raw-to-expected sample supplied; every generated rule remains unverified")
	}
	return result
}

func migrationFullSourceRange(content []byte) migration.SourceRange {
	line, column := 1, 1
	for _, char := range content {
		if char == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return migration.SourceRange{
		Start: migration.SourcePosition{Line: 1, Column: 1, Offset: 0},
		End:   migration.SourcePosition{Line: line, Column: column, Offset: len(content)},
	}
}

func assessNLIRegression(ctx context.Context, result *migration.MigrationResult, regression parserpolicy.RegressionCase) {
	outcome := migration.RegressionOutcome{Name: regression.Name, ExpectedParser: regression.ExpectedParser}
	executor, err := parserpolicy.NewParserPackExecutor(result.ParserPack)
	if err != nil {
		outcome.Error = "normalized parser executor initialization failed"
	} else {
		execution, executeErr := executor.Execute(&parserpolicy.ExecutionRequest{Input: regression.Input, Service: regression.Service, SourceType: regression.SourceType, Context: ctx})
		if executeErr != nil {
			outcome.Error = "candidate execution failed within the declarative parser runtime"
		} else if execution == nil || execution.Quarantined || execution.EventPayload == nil {
			outcome.Error = "generated parser quarantined the candidate"
		} else {
			outcome.ActualParser = execution.ParserID
			compareMigrationPayload(regression.ExpectedPayload, execution.EventPayload.Fields, &outcome)
			if regression.ExpectedParser != "*" && regression.ExpectedParser != execution.ParserID && regression.ExpectedParser != execution.RuleID {
				outcome.Error = "candidate selected a different parser rule"
			}
			if regression.ExpectedRoute != "" && (execution.ParseTelemetry == nil || regression.ExpectedRoute != execution.ParseTelemetry.Route) {
				outcome.Error = "candidate selected a different route"
			}
			for index := range result.RuleAssessments {
				if result.RuleAssessments[index].RuleID == execution.RuleID {
					result.RuleAssessments[index].SampleCount = 1
				}
			}
		}
	}
	outcome.Passed = outcome.Error == "" && len(outcome.MissingFields) == 0 && len(outcome.ExtraFields) == 0 && len(outcome.TypeMismatches) == 0 && len(outcome.ValueMismatch) == 0
	result.RegressionOutcomes = []migration.RegressionOutcome{outcome}
	if outcome.Passed {
		result.ParityStatus = migration.ParityPassed
	} else {
		result.ParityStatus = migration.ParityFailed
	}
	for index := range result.RuleAssessments {
		if result.RuleAssessments[index].SampleCount > 0 {
			result.RuleAssessments[index].ParityStatus = result.ParityStatus
		}
	}
}

func compareMigrationPayload(expected, actual map[string]any, outcome *migration.RegressionOutcome) {
	expectedMap := normalizeMigrationObject(expected)
	actualMap := normalizeMigrationObject(actual)
	for key, expectedValue := range expectedMap {
		actualValue, exists := actualMap[key]
		if !exists {
			outcome.MissingFields = append(outcome.MissingFields, key)
			continue
		}
		if reflect.TypeOf(expectedValue) != reflect.TypeOf(actualValue) {
			outcome.TypeMismatches = append(outcome.TypeMismatches, key)
			continue
		}
		if !reflect.DeepEqual(expectedValue, actualValue) {
			outcome.ValueMismatch = append(outcome.ValueMismatch, key)
		}
	}
	for key := range actualMap {
		if _, exists := expectedMap[key]; !exists {
			outcome.ExtraFields = append(outcome.ExtraFields, key)
		}
	}
	sort.Strings(outcome.MissingFields)
	sort.Strings(outcome.ExtraFields)
	sort.Strings(outcome.TypeMismatches)
	sort.Strings(outcome.ValueMismatch)
}

func normalizeMigrationObject(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	object := map[string]any{}
	_ = decoder.Decode(&object)
	return object
}

func prepareMigrationDrafts(results []migration.MigrationResult, createDraft bool) ([]preparedMigrationDraft, []migrationGeneratedPack, error) {
	drafts := []preparedMigrationDraft{}
	generated := []migrationGeneratedPack{}
	for index := range results {
		pack := results[index].ParserPack
		if pack == nil || !results[index].Success {
			continue
		}
		pack.RegressionCases = nil
		pack.ReviewState = parserpolicy.ReviewStateDraft
		pack.ActivatedAt = nil
		pack.Revision = 1
		pack.SHA256 = ""
		if err := parserpolicy.SealParserPack(pack); err != nil {
			return nil, nil, fmt.Errorf("generated parser pack could not be sealed")
		}
		validation := parserpolicy.ValidateParserPack(pack)
		if !validation.Valid {
			return nil, nil, fmt.Errorf("generated parser pack failed parser-policy validation")
		}
		payload, err := json.Marshal(pack)
		if err != nil || len(payload) > migrationMaxStoredPayload {
			return nil, nil, fmt.Errorf("generated parser pack exceeds the bounded persistence limit")
		}
		var declarative any
		if json.Unmarshal(payload, &declarative) != nil || unsafeDeclarative(declarative) != nil {
			return nil, nil, fmt.Errorf("generated parser pack failed declarative safety validation")
		}
		base := safeID(pack.ID)
		if len(base) > 48 {
			base = base[:48]
		}
		draftID := fmt.Sprintf("imported-%s-%s", base, results[index].SourceSHA256[:12])
		info := migrationGeneratedPack{
			SourceFile: results[index].SourceFile, ID: pack.ID, Version: pack.Version, Revision: pack.Revision,
			SHA256: pack.SHA256, SourceHash: pack.SourceHash, RuleCount: len(pack.Rules), ReviewState: string(pack.ReviewState),
			Activated: false, RegressionCasesRetained: false,
		}
		if createDraft {
			info.DraftID = draftID
			drafts = append(drafts, preparedMigrationDraft{ID: draftID, Payload: payload})
		}
		generated = append(generated, info)
	}
	return drafts, generated, nil
}

func completeCandidateAssessment(assessment *migrationCandidateAssessment, results []migration.MigrationResult) {
	assessment.Outcomes = []migration.RegressionOutcome{}
	for _, result := range results {
		assessment.Outcomes = append(assessment.Outcomes, result.RegressionOutcomes...)
		if result.ParityStatus == migration.ParityFailed {
			assessment.ParityStatus = migration.ParityFailed
		} else if result.ParityStatus == migration.ParityPassed && assessment.ParityStatus != migration.ParityFailed {
			assessment.ParityStatus = migration.ParityPassed
		}
	}
}

func buildMigrationImportReport(filename, kind string, content []byte, sourceHash string, results []migration.MigrationResult, archiveEntries []migrationArchiveEntry, generated []migrationGeneratedPack, assessment *migrationCandidateAssessment) migrationImportReport {
	compatibility := migrationCompatibility{
		OperationsByKind: map[string]int{}, TranslatedByKind: map[string]int{}, UnsupportedByKind: map[string]int{},
	}
	report := migrationImportReport{
		SchemaVersion: "nli.migration.v1", Filename: filename, SourceType: kind, SourceHash: sourceHash,
		SourceSize: int64(len(content)), SourceMediaType: http.DetectContentType(content), SourceRetained: false,
		RawPayloadRetained: false, ManualApprovalRequired: true, AutoActivated: false,
		Supported: []migrationSupportedConstruct{}, Unsupported: []migrationUnsupportedConstruct{},
		Results: []migrationResultView{}, GeneratedPacks: generated, ArchiveEntries: archiveEntries,
		CandidateAssessment: assessment,
	}
	packBySource := map[string]*migrationGeneratedPack{}
	for index := range report.GeneratedPacks {
		packBySource[report.GeneratedPacks[index].SourceFile] = &report.GeneratedPacks[index]
		if report.GeneratedPacks[index].DraftID != "" {
			report.DraftIDs = append(report.DraftIDs, report.GeneratedPacks[index].DraftID)
		}
	}
	if len(report.DraftIDs) > 0 {
		report.DraftID = report.DraftIDs[0]
	}
	repairs := map[string]struct{}{}
	compatibility.SourceFiles = len(results)
	for _, result := range results {
		if result.Parsed {
			compatibility.ParsedFiles++
		}
		if result.Success && result.ParserPack != nil {
			compatibility.TranslatedFiles++
		}
		if !result.Success {
			compatibility.FailedFiles++
		}
		if result.ParityStatus == migration.ParityPassed {
			compatibility.VerifiedFiles++
		} else if result.Success {
			compatibility.UnverifiedFiles++
		}
		view := migrationResultView{
			SourceFile: result.SourceFile, SourceFormat: result.SourceFormat, SourceSHA256: result.SourceSHA256,
			Parsed: result.Parsed, Success: result.Success, GeneratedPack: packBySource[result.SourceFile],
			Operations: result.Operations, LineMappings: result.LineMappings,
			UnsupportedConstructs: result.UnsupportedConstructs, RuleAssessments: result.RuleAssessments,
			RegressionOutcomes: result.RegressionOutcomes, ParityStatus: result.ParityStatus,
			Warnings: boundedMigrationMessages(result.Warnings), Errors: safeMigrationErrors(result),
		}
		report.Results = append(report.Results, view)
		for _, operation := range result.Operations {
			compatibility.OperationsByKind[operation.Kind]++
			if operation.Status == "translated" {
				compatibility.TranslatedByKind[operation.Kind]++
				report.Supported = append(report.Supported, migrationSupportedConstruct{
					SourceFile: result.SourceFile, Construct: operation.Kind, Line: operation.Range.Start.Line,
					IRRuleID: operation.IRRuleID, IRStageID: operation.IRStageID,
				})
			}
		}
		for _, unsupported := range result.UnsupportedConstructs {
			compatibility.UnsupportedByKind[unsupported.Type]++
			report.Unsupported = append(report.Unsupported, migrationUnsupportedConstruct{
				SourceFile: result.SourceFile, Type: unsupported.Type, Description: unsupported.Description,
				Range: unsupported.Range, Severity: unsupported.Severity, Repair: unsupported.Repair,
			})
			if unsupported.Repair != "" {
				repairs[unsupported.Repair] = struct{}{}
			}
		}
	}
	for _, entry := range archiveEntries {
		if entry.Status != "unsupported" {
			continue
		}
		compatibility.SourceFiles++
		compatibility.FailedFiles++
		compatibility.UnsupportedByKind["archive.unsupported_file_type"]++
		report.Unsupported = append(report.Unsupported, migrationUnsupportedConstruct{
			SourceFile: entry.Name, Type: "archive.unsupported_file_type",
			Description: "archive entry is not a supported Alloy/River, Vector, or NLI ParserPack source",
			Severity:    "error", Repair: "remove the file or convert it to a supported declarative parser source",
		})
		repairs["remove the file or convert it to a supported declarative parser source"] = struct{}{}
	}
	for repair := range repairs {
		compatibility.RequiredRepairs = append(compatibility.RequiredRepairs, repair)
	}
	sort.Strings(compatibility.RequiredRepairs)
	report.SupportedCount = len(report.Supported)
	report.UnsupportedCount = len(report.Unsupported)
	report.Compatibility = compatibility
	switch {
	case compatibility.FailedFiles > 0:
		report.ParityStatus = migration.ParityFailed
	case compatibility.TranslatedFiles > 0 && compatibility.VerifiedFiles == compatibility.TranslatedFiles:
		report.ParityStatus = migration.ParityPassed
	default:
		report.ParityStatus = migration.ParityUnverified
	}
	return report
}

func boundedMigrationMessages(messages []string) []string {
	if len(messages) == 0 {
		return nil
	}
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		message = strings.Join(strings.Fields(message), " ")
		if len(message) > 512 {
			message = message[:512]
		}
		out = append(out, message)
	}
	return out
}

func safeMigrationErrors(result migration.MigrationResult) []string {
	if len(result.Errors) == 0 {
		return nil
	}
	if !result.Parsed {
		return []string{fmt.Sprintf("%s source failed syntax and subset validation", result.SourceFormat)}
	}
	return []string{"source did not produce a valid parser-policy draft"}
}

func sealMigrationReport(report *migrationImportReport) (json.RawMessage, error) {
	core, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(core)
	report.ReportHash = hex.EncodeToString(digest[:])
	return json.Marshal(report)
}
