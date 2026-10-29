package migration

import parserpolicy "nexora.local/nli-v20/parser-policy"

const (
	ParityUnverified = "unverified"
	ParityPassed     = "passed"
	ParityFailed     = "failed"
)

// UploadedFile is an inert parser source. Content is parsed, never executed.
type UploadedFile struct {
	Name        string                        `json:"name"`
	Content     []byte                        `json:"-"`
	Size        int64                         `json:"size"`
	MimeType    string                        `json:"mime_type,omitempty"`
	Format      string                        `json:"format,omitempty"`
	Checksum    string                        `json:"checksum,omitempty"`
	Regressions []parserpolicy.RegressionCase `json:"regressions,omitempty"`
}

// SourcePosition uses one-based lines/columns and zero-based byte offsets.
type SourcePosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
	Offset int `json:"offset"`
}

// SourceRange identifies the exact source construct represented by an IR item.
type SourceRange struct {
	Start SourcePosition `json:"start"`
	End   SourcePosition `json:"end"`
}

// LineMapping preserves source-to-IR provenance without adding unrecognised
// fields to parser-policy stage configurations.
type LineMapping struct {
	SourceFile string      `json:"source_file"`
	Construct  string      `json:"construct"`
	IRRuleID   string      `json:"ir_rule_id,omitempty"`
	IRStageID  string      `json:"ir_stage_id,omitempty"`
	Range      SourceRange `json:"range"`
	Status     string      `json:"status"` // translated, metadata, unsupported
}

// UnsupportedConstruct records semantics that cannot be represented exactly.
type UnsupportedConstruct struct {
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Range       SourceRange `json:"range"`
	Severity    string      `json:"severity"` // info, warning, error
	Repair      string      `json:"repair,omitempty"`
}

// OperationRecord is a deterministic structural inventory of parsed source.
type OperationRecord struct {
	Kind      string      `json:"kind"`
	Name      string      `json:"name,omitempty"`
	Range     SourceRange `json:"range"`
	IRRuleID  string      `json:"ir_rule_id,omitempty"`
	IRStageID string      `json:"ir_stage_id,omitempty"`
	Status    string      `json:"status"`
}

// RuleAssessment makes absence of sample events explicit per generated rule.
type RuleAssessment struct {
	RuleID       string `json:"rule_id"`
	Priority     int    `json:"priority"`
	SampleCount  int    `json:"sample_count"`
	ParityStatus string `json:"parity_status"`
}

// RegressionOutcome is an exact structural comparison result.
type RegressionOutcome struct {
	Name           string   `json:"name"`
	Passed         bool     `json:"passed"`
	ExpectedParser string   `json:"expected_parser"`
	ActualParser   string   `json:"actual_parser,omitempty"`
	MissingFields  []string `json:"missing_fields,omitempty"`
	ExtraFields    []string `json:"extra_fields,omitempty"`
	TypeMismatches []string `json:"type_mismatches,omitempty"`
	ValueMismatch  []string `json:"value_mismatches,omitempty"`
	Error          string   `json:"error,omitempty"`
}

// MigrationResult describes one independently parsed source.
type MigrationResult struct {
	SourceFile            string                   `json:"source_file"`
	SourceFormat          string                   `json:"source_format"`
	SourceSHA256          string                   `json:"source_sha256"`
	Parsed                bool                     `json:"parsed"`
	Success               bool                     `json:"success"`
	ParserPack            *parserpolicy.ParserPack `json:"parser_pack,omitempty"`
	Operations            []OperationRecord        `json:"operations"`
	LineMappings          []LineMapping            `json:"line_mappings"`
	UnsupportedConstructs []UnsupportedConstruct   `json:"unsupported_constructs"`
	RuleAssessments       []RuleAssessment         `json:"rule_assessments"`
	RegressionOutcomes    []RegressionOutcome      `json:"regression_outcomes,omitempty"`
	ParityStatus          string                   `json:"parity_status"`
	Warnings              []string                 `json:"warnings,omitempty"`
	Errors                []string                 `json:"errors,omitempty"`
}

// BatchResult is deterministic for identical ordered input bytes.
type BatchResult struct {
	TotalFiles      int                 `json:"total_files"`
	ParsedFiles     int                 `json:"parsed_files"`
	TranslatedFiles int                 `json:"translated_files"`
	FailedFiles     int                 `json:"failed_files"`
	VerifiedFiles   int                 `json:"verified_files"`
	Results         []MigrationResult   `json:"results"`
	Summary         CompatibilityReport `json:"summary"`
}

// CompatibilityReport separates structural translation from measured parity.
type CompatibilityReport struct {
	SourceFiles          int                    `json:"source_files"`
	ParsedFiles          int                    `json:"parsed_files"`
	TranslatedFiles      int                    `json:"translated_files"`
	VerifiedFiles        int                    `json:"verified_files"`
	UnverifiedFiles      int                    `json:"unverified_files"`
	FailedFiles          int                    `json:"failed_files"`
	OperationsByKind     map[string]int         `json:"operations_by_kind"`
	TranslatedByKind     map[string]int         `json:"translated_by_kind"`
	UnsupportedByKind    map[string]int         `json:"unsupported_by_kind"`
	ParserFamilyAnalysis map[string]FamilyStats `json:"parser_family_analysis"`
	RequiredRepairs      []string               `json:"required_repairs,omitempty"`
}

// FamilyStats is derived from explicit service selectors and fixture metadata.
type FamilyStats struct {
	Sources     int `json:"sources"`
	Parsed      int `json:"parsed"`
	Translated  int `json:"translated"`
	Verified    int `json:"verified"`
	Unverified  int `json:"unverified"`
	Failed      int `json:"failed"`
	Unsupported int `json:"unsupported"`
}
