package migration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	parserpolicy "nexora.local/nli-v20/parser-policy"
)

const (
	defaultMaxFileBytes  = int64(10 << 20)
	defaultMaxFiles      = 100
	defaultMaxTotalBytes = int64(50 << 20)
)

var deterministicMigrationTime = time.Unix(0, 0).UTC()

// BatchProcessor applies strict upload and archive limits.
type BatchProcessor struct {
	MaxFileBytes  int64
	MaxFiles      int
	MaxTotalBytes int64
}

func NewBatchProcessor() *BatchProcessor {
	return &BatchProcessor{MaxFileBytes: defaultMaxFileBytes, MaxFiles: defaultMaxFiles, MaxTotalBytes: defaultMaxTotalBytes}
}

func (processor *BatchProcessor) limits() (int64, int, int64) {
	if processor == nil {
		processor = NewBatchProcessor()
	}
	fileLimit, fileCount, totalLimit := processor.MaxFileBytes, processor.MaxFiles, processor.MaxTotalBytes
	if fileLimit <= 0 {
		fileLimit = defaultMaxFileBytes
	}
	if fileCount <= 0 {
		fileCount = defaultMaxFiles
	}
	if totalLimit <= 0 {
		totalLimit = defaultMaxTotalBytes
	}
	return fileLimit, fileCount, totalLimit
}

func (processor *BatchProcessor) ProcessBatch(files []UploadedFile) (*BatchResult, error) {
	fileLimit, fileCount, totalLimit := processor.limits()
	if len(files) == 0 {
		return nil, fmt.Errorf("migration batch is empty")
	}
	if len(files) > fileCount {
		return nil, fmt.Errorf("migration batch contains %d files; maximum is %d", len(files), fileCount)
	}
	var total int64
	for _, file := range files {
		total += int64(len(file.Content))
		if total > totalLimit {
			return nil, fmt.Errorf("migration batch expands beyond %d bytes", totalLimit)
		}
	}

	batch := &BatchResult{TotalFiles: len(files), Results: make([]MigrationResult, 0, len(files))}
	for _, file := range files {
		result := processSource(file, fileLimit)
		batch.Results = append(batch.Results, result)
		if result.Parsed {
			batch.ParsedFiles++
		}
		if result.ParserPack != nil && result.Success {
			batch.TranslatedFiles++
		}
		if !result.Success {
			batch.FailedFiles++
		}
		if result.ParityStatus == ParityPassed {
			batch.VerifiedFiles++
		}
	}
	batch.Summary = buildCompatibilityReport(batch.Results)
	return batch, nil
}

func processSource(file UploadedFile, maxBytes int64) MigrationResult {
	result := MigrationResult{SourceFile: file.Name, ParityStatus: ParityUnverified}
	cleanName, err := safeSourceName(file.Name)
	if err != nil {
		result.Errors = []string{err.Error()}
		return result
	}
	result.SourceFile = cleanName
	actualSize := int64(len(file.Content))
	if actualSize == 0 {
		result.Errors = []string{"source is empty"}
		return result
	}
	if actualSize > maxBytes {
		result.Errors = []string{fmt.Sprintf("source is %d bytes; maximum is %d", actualSize, maxBytes)}
		return result
	}
	if file.Size > 0 && file.Size != actualSize {
		result.Errors = []string{fmt.Sprintf("declared size %d does not match content size %d", file.Size, actualSize)}
		return result
	}
	digest := sha256.Sum256(file.Content)
	result.SourceSHA256 = hex.EncodeToString(digest[:])
	if file.Checksum != "" {
		provided, decodeErr := hex.DecodeString(file.Checksum)
		if decodeErr != nil || len(provided) != sha256.Size || subtle.ConstantTimeCompare(provided, digest[:]) != 1 {
			result.Errors = []string{"source checksum does not match content SHA-256"}
			return result
		}
	}
	format := normalizeFormat(file.Format)
	if format == "" {
		format = detectFormat(cleanName, file.Content)
	}
	result.SourceFormat = format
	file.Name, file.Size, file.Checksum, file.Format = cleanName, actualSize, result.SourceSHA256, format

	switch format {
	case "alloy":
		result = translateAlloySource(file)
	case "vector":
		result = translateVectorSource(file)
	default:
		result.Errors = []string{fmt.Sprintf("unsupported migration format for %q", cleanName)}
		result.SourceFile, result.SourceFormat, result.SourceSHA256 = cleanName, format, hex.EncodeToString(digest[:])
		return result
	}
	result.SourceSHA256 = hex.EncodeToString(digest[:])
	if result.ParserPack == nil {
		if result.Parsed && len(result.Errors) == 0 {
			result.Success = true
		}
		sortMigrationResult(&result)
		return result
	}

	result.ParserPack.SourceHash = result.SourceSHA256
	result.ParserPack.SourceType = "migrated"
	result.ParserPack.ReviewState = parserpolicy.ReviewStateDraft
	result.ParserPack.CreatedAt = deterministicMigrationTime
	result.ParserPack.ActivatedAt = nil
	result.ParserPack.RegressionCases = append([]parserpolicy.RegressionCase(nil), file.Regressions...)
	result.ParserPack.SHA256 = ""
	if err := parserpolicy.SealParserPack(result.ParserPack); err != nil {
		result.Errors = append(result.Errors, "seal generated parser pack: "+err.Error())
		result.Success = false
		sortMigrationResult(&result)
		return result
	}
	validation := parserpolicy.ValidateParserPack(result.ParserPack)
	if !validation.Valid {
		result.Errors = append(result.Errors, "generated parser pack is invalid: "+strings.Join(validation.Errors, "; "))
		result.Success = false
		sortMigrationResult(&result)
		return result
	}
	result.Success = true
	assessRegressions(&result)
	sortMigrationResult(&result)
	return result
}

func assessRegressions(result *MigrationResult) {
	for _, rule := range result.ParserPack.Rules {
		result.RuleAssessments = append(result.RuleAssessments, RuleAssessment{
			RuleID: rule.ID, Priority: rule.Priority, ParityStatus: ParityUnverified,
		})
	}
	if len(result.ParserPack.RegressionCases) == 0 {
		result.ParityStatus = ParityUnverified
		result.Warnings = append(result.Warnings, "no raw-to-expected samples supplied; every generated rule remains unverified")
		return
	}
	executor, err := parserpolicy.NewParserPackExecutor(result.ParserPack)
	if err != nil {
		result.ParityStatus = ParityFailed
		result.RegressionOutcomes = append(result.RegressionOutcomes, RegressionOutcome{Name: "executor", Error: err.Error()})
		return
	}
	allPassed := true
	for _, regression := range result.ParserPack.RegressionCases {
		outcome := RegressionOutcome{Name: regression.Name, ExpectedParser: regression.ExpectedParser}
		execution, executeErr := executor.Execute(&parserpolicy.ExecutionRequest{
			Input: regression.Input, Service: regression.Service, SourceType: regression.SourceType, Context: context.Background(),
		})
		if executeErr != nil {
			outcome.Error = executeErr.Error()
		} else if execution == nil || execution.Quarantined || execution.EventPayload == nil {
			outcome.Error = "generated parser quarantined the sample"
		} else {
			outcome.ActualParser = execution.ParserID
			comparePayload(regression.ExpectedPayload, execution.EventPayload, &outcome)
			if regression.ExpectedParser != "*" && regression.ExpectedParser != execution.ParserID && regression.ExpectedParser != execution.RuleID {
				outcome.Error = fmt.Sprintf("parser mismatch: got %s", execution.ParserID)
			}
		}
		outcome.Passed = outcome.Error == "" && len(outcome.MissingFields) == 0 && len(outcome.ExtraFields) == 0 && len(outcome.TypeMismatches) == 0 && len(outcome.ValueMismatch) == 0
		if !outcome.Passed {
			allPassed = false
		}
		result.RegressionOutcomes = append(result.RegressionOutcomes, outcome)
		for index := range result.RuleAssessments {
			assessment := &result.RuleAssessments[index]
			if outcome.ActualParser == result.ParserPack.ID+"/"+assessment.RuleID || regression.ExpectedParser == assessment.RuleID {
				assessment.SampleCount++
				if outcome.Passed {
					assessment.ParityStatus = ParityPassed
				} else {
					assessment.ParityStatus = ParityFailed
				}
			}
		}
	}
	if allPassed {
		result.ParityStatus = ParityPassed
	} else {
		result.ParityStatus = ParityFailed
	}
}

func comparePayload(expected map[string]interface{}, actual interface{}, outcome *RegressionOutcome) {
	expectedMap := normalizeJSONObject(expected)
	actualMap := normalizeJSONObject(actual)
	for key, expectedValue := range expectedMap {
		actualValue, exists := actualMap[key]
		if !exists {
			outcome.MissingFields = append(outcome.MissingFields, key)
			continue
		}
		if reflect.TypeOf(expectedValue) != reflect.TypeOf(actualValue) {
			outcome.TypeMismatches = append(outcome.TypeMismatches, fmt.Sprintf("%s: expected %T, got %T", key, expectedValue, actualValue))
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

func normalizeJSONObject(value interface{}) map[string]interface{} {
	data, _ := json.Marshal(value)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]interface{}
	_ = decoder.Decode(&object)
	return object
}

func (processor *BatchProcessor) ProcessArchive(data []byte) (*BatchResult, error) {
	fileLimit, fileCount, totalLimit := processor.limits()
	if len(data) == 0 {
		return nil, fmt.Errorf("migration archive is empty")
	}
	if int64(len(data)) > totalLimit {
		return nil, fmt.Errorf("migration archive exceeds %d bytes", totalLimit)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("parse migration ZIP: %w", err)
	}
	if len(reader.File) > fileCount {
		return nil, fmt.Errorf("migration archive has %d entries; maximum is %d", len(reader.File), fileCount)
	}
	files := make([]UploadedFile, 0, len(reader.File))
	var expanded int64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.Mode()&0o170000 == 0o120000 {
			return nil, fmt.Errorf("migration archive contains symlink %q", entry.Name)
		}
		name, nameErr := safeSourceName(entry.Name)
		if nameErr != nil {
			return nil, nameErr
		}
		if entry.UncompressedSize64 > uint64(fileLimit) {
			return nil, fmt.Errorf("archive entry %q exceeds %d bytes", name, fileLimit)
		}
		stream, openErr := entry.Open()
		if openErr != nil {
			return nil, fmt.Errorf("open archive entry %q: %w", name, openErr)
		}
		content, readErr := io.ReadAll(io.LimitReader(stream, fileLimit+1))
		closeErr := stream.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read archive entry %q: %w", name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close archive entry %q: %w", name, closeErr)
		}
		if int64(len(content)) > fileLimit {
			return nil, fmt.Errorf("archive entry %q exceeds %d bytes", name, fileLimit)
		}
		expanded += int64(len(content))
		if expanded > totalLimit {
			return nil, fmt.Errorf("migration archive expands beyond %d bytes", totalLimit)
		}
		format := detectFormat(name, content)
		if format == "" {
			continue
		}
		digest := sha256.Sum256(content)
		files = append(files, UploadedFile{Name: name, Content: content, Size: int64(len(content)), Format: format, Checksum: hex.EncodeToString(digest[:])})
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("migration archive contains no supported parser sources")
	}
	return processor.ProcessBatch(files)
}

func TranslateAlloy(content []byte, filename string, regressions ...parserpolicy.RegressionCase) (*MigrationResult, error) {
	return translateOne(content, filename, "alloy", regressions)
}

func TranslateVector(content []byte, filename string, regressions ...parserpolicy.RegressionCase) (*MigrationResult, error) {
	return translateOne(content, filename, "vector", regressions)
}

func translateOne(content []byte, filename, format string, regressions []parserpolicy.RegressionCase) (*MigrationResult, error) {
	batch, err := NewBatchProcessor().ProcessBatch([]UploadedFile{{Name: filename, Content: content, Size: int64(len(content)), Format: format, Regressions: regressions}})
	if err != nil {
		return nil, err
	}
	result := &batch.Results[0]
	if !result.Success {
		return result, fmt.Errorf("migration failed: %s", strings.Join(result.Errors, "; "))
	}
	return result, nil
}

func normalizeFormat(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "alloy", "river", "hcl":
		return "alloy"
	case "vector", "toml", "vrl":
		return "vector"
	default:
		return ""
	}
}

func detectFormat(filename string, content []byte) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".alloy", ".river", ".hcl":
		return "alloy"
	case ".toml", ".vrl":
		return "vector"
	}
	text := string(content)
	if strings.Contains(text, "loki.process") || strings.Contains(text, "loki.source.file") || strings.Contains(text, "import.file") {
		return "alloy"
	}
	if strings.Contains(text, "[transforms.") || strings.Contains(text, "[sources.") {
		return "vector"
	}
	return ""
}

func safeSourceName(name string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	if normalized == "" || strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("unsafe source filename %q", name)
	}
	cleaned := path.Clean(normalized)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("unsafe source filename %q", name)
	}
	return cleaned, nil
}

func sortMigrationResult(result *MigrationResult) {
	sort.SliceStable(result.LineMappings, func(i, j int) bool {
		a, b := result.LineMappings[i], result.LineMappings[j]
		if a.Range.Start.Offset != b.Range.Start.Offset {
			return a.Range.Start.Offset < b.Range.Start.Offset
		}
		if a.IRRuleID != b.IRRuleID {
			return a.IRRuleID < b.IRRuleID
		}
		return a.IRStageID < b.IRStageID
	})
	sort.SliceStable(result.UnsupportedConstructs, func(i, j int) bool {
		a, b := result.UnsupportedConstructs[i], result.UnsupportedConstructs[j]
		if a.Range.Start.Offset != b.Range.Start.Offset {
			return a.Range.Start.Offset < b.Range.Start.Offset
		}
		return a.Type < b.Type
	})
	sort.Strings(result.Warnings)
	sort.Strings(result.Errors)
}

func buildCompatibilityReport(results []MigrationResult) CompatibilityReport {
	report := CompatibilityReport{
		SourceFiles: len(results), OperationsByKind: map[string]int{}, TranslatedByKind: map[string]int{},
		UnsupportedByKind: map[string]int{}, ParserFamilyAnalysis: map[string]FamilyStats{},
	}
	repairs := map[string]struct{}{}
	for _, result := range results {
		if result.Parsed {
			report.ParsedFiles++
		}
		if result.Success && result.ParserPack != nil {
			report.TranslatedFiles++
		}
		if !result.Success {
			report.FailedFiles++
		}
		if result.ParityStatus == ParityPassed {
			report.VerifiedFiles++
		} else if result.Success {
			report.UnverifiedFiles++
		}
		for _, operation := range result.Operations {
			report.OperationsByKind[operation.Kind]++
			if operation.Status == "translated" {
				report.TranslatedByKind[operation.Kind]++
			}
		}
		for _, unsupported := range result.UnsupportedConstructs {
			report.UnsupportedByKind[unsupported.Type]++
			if unsupported.Repair != "" {
				repairs[unsupported.Repair] = struct{}{}
			}
		}
		family := familyForResult(result)
		stats := report.ParserFamilyAnalysis[family]
		stats.Sources++
		if result.Parsed {
			stats.Parsed++
		}
		if result.Success && result.ParserPack != nil {
			stats.Translated++
		}
		if result.ParityStatus == ParityPassed {
			stats.Verified++
		} else if result.Success {
			stats.Unverified++
		}
		if !result.Success {
			stats.Failed++
		}
		stats.Unsupported += len(result.UnsupportedConstructs)
		report.ParserFamilyAnalysis[family] = stats
	}
	for repair := range repairs {
		report.RequiredRepairs = append(report.RequiredRepairs, repair)
	}
	sort.Strings(report.RequiredRepairs)
	return report
}

func familyForResult(result MigrationResult) string {
	name := strings.ToLower(result.SourceFile)
	for _, item := range []struct{ needle, family string }{
		{"smtp-in", "SMTP-IN"}, {"smtp-out", "SMTP-OUT"}, {"webtop", "WEBTOP-WEBMAIL"},
		{"imap", "IMAP"}, {"pop", "POP"}, {"mta", "MTA"}, {"pabd", "PABD-PSS"}, {"capd", "CAPD-MSS"},
	} {
		if strings.Contains(name, item.needle) {
			return item.family
		}
	}
	return "ORCHESTRATION"
}
