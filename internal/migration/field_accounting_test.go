package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	parserpolicy "nexora.local/nli-v20/parser-policy"
)

const sampleFieldAccountingReportEnv = "NLI_FIELD_ACCOUNTING_REPORT"

type fieldAccountingReport struct {
	SchemaVersion  int                      `json:"schema_version"`
	Verdict        string                   `json:"verdict"`
	CountSemantics fieldAccountingSemantics `json:"count_semantics"`
	KeyInventory   fieldAccountingKeys      `json:"known_key_inventory"`
	Totals         fieldAccountingTotals    `json:"totals"`
	Families       []fieldAccountingFamily  `json:"families"`
	Records        []fieldAccountingRecord  `json:"records"`
}

type fieldAccountingKeys struct {
	SourceMember          string `json:"source_member"`
	SourceSHA256          string `json:"source_sha256"`
	DeclaredFamilyKeys    int    `json:"declared_family_keys"`
	CoveredFamilyKeys     int    `json:"covered_family_keys"`
	ReviewedExtensionKeys int    `json:"reviewed_extension_keys"`
	UnmappedFamilyKeys    int    `json:"unmapped_family_keys"`
}

type fieldAccountingSemantics struct {
	RawOccurrences string `json:"raw_occurrences"`
	ComparableRaw  string `json:"comparable_raw_fields"`
	GeneratedJSON  string `json:"generated_json_fields"`
	DuplicateKeys  string `json:"duplicate_keys"`
}

type fieldAccountingTotals struct {
	Records               int `json:"records"`
	AccountedRecords      int `json:"accounted_records"`
	ParsedRecords         int `json:"parsed_records"`
	ExactParityRecords    int `json:"exact_parity_records"`
	PassingRecords        int `json:"passing_records"`
	FailingRecords        int `json:"failing_records"`
	QuarantinedRecords    int `json:"quarantined_records"`
	ExpectedQuarantined   int `json:"expected_quarantined_records"`
	UnexpectedQuarantined int `json:"unexpected_quarantined_records"`
	RawOccurrences        int `json:"raw_field_occurrences"`
	ComparableRawFields   int `json:"comparable_raw_fields"`
	GeneratedJSONFields   int `json:"generated_json_fields"`
	ParsedRawFields       int `json:"parsed_comparable_raw_fields"`
	ParsedJSONFields      int `json:"parsed_generated_json_fields"`
	DuplicateOccurrences  int `json:"duplicate_occurrences"`
}

type fieldAccountingFamily struct {
	Family               string `json:"family"`
	Records              int    `json:"records"`
	PassingRecords       int    `json:"passing_records"`
	FailingRecords       int    `json:"failing_records"`
	RawOccurrences       int    `json:"raw_field_occurrences"`
	ComparableRawFields  int    `json:"comparable_raw_fields"`
	GeneratedJSONFields  int    `json:"generated_json_fields"`
	DuplicateOccurrences int    `json:"duplicate_occurrences"`
}

type fieldAccountingRecord struct {
	Corpus                string         `json:"corpus"`
	SourceLine            int            `json:"source_line"`
	Family                string         `json:"family"`
	RecordSHA256          string         `json:"record_sha256"`
	ParseStatus           string         `json:"parse_status"`
	RawStructuralFields   int            `json:"raw_structural_fields"`
	RawKVOccurrences      int            `json:"raw_kv_occurrences"`
	RawFieldOccurrences   int            `json:"raw_field_occurrences"`
	ComparableRawFields   int            `json:"comparable_raw_fields"`
	GeneratedJSONFields   int            `json:"generated_json_fields"`
	TelemetryOutputFields int            `json:"telemetry_output_fields"`
	DuplicatePolicy       string         `json:"duplicate_policy"`
	DuplicateOccurrences  int            `json:"duplicate_occurrences"`
	DuplicateKeys         map[string]int `json:"duplicate_keys,omitempty"`
	UnreviewedKeys        []string       `json:"unreviewed_keys,omitempty"`
	ExpectedDisposition   string         `json:"expected_disposition"`
	DispositionReason     string         `json:"disposition_reason,omitempty"`
	Errors                []string       `json:"errors,omitempty"`
	Pass                  bool           `json:"pass"`
}

type expectedFieldAccountingDisposition struct {
	SHA256 string
	Reason string
	Code   string
}

var expectedFieldAccountingQuarantine = map[string]expectedFieldAccountingDisposition{
	"dt/All_Dummy_sample_logs.txt:137": {SHA256: "1ccd5d5d43c56e5a287f12e82b94b9980ef0ca60fb6bd49d43b7259b224bad44", Reason: "value-less CAPD schema specimen", Code: "no_eligible_rule"},
	"dt/All_Dummy_sample_logs.txt:138": {SHA256: "4878ebc6fe06c37cc7fd65ae0dfecfca1a360ce6c4b5491afa314884b4b1652e", Reason: "value-less CAPD schema specimen", Code: "no_eligible_rule"},
	"dt/All_Dummy_sample_logs.txt:139": {SHA256: "6dbfc8b26f9c01071ad3942cadbe8ab77f6cc6b63b41e9bebab7c22dc7571932", Reason: "value-less CAPD schema specimen", Code: "no_eligible_rule"},
	"dt/All_Dummy_sample_logs.txt:140": {SHA256: "bb88d6b1612a83e68c605be5dba1bc2667bd577ea9e5371be87011f4ece7e8b9", Reason: "value-less CAPD schema specimen", Code: "no_eligible_rule"},
	"1st_test_sample_logs.txt:50":      {SHA256: "9af3cf7bf7f8916efce6aa55984914e997b655cebbcfec86937e9c0867fa8856", Reason: "truncated SMTP-OUT record missing event token", Code: "static_parse"},
}

type fieldAccountingInput struct {
	Corpus     string
	SourceLine int
	Family     string
	Raw        string
}

// TestSampleLogCorpusFieldAccounting is the release gate for the complete
// supplied sample corpus: the 678 records in dt.zip plus the 50 records in the
// separately supplied sample file. It deliberately derives the source count
// without consulting parser telemetry or the generated payload.
//
// A JSON object cannot contain repeated member names. Therefore the comparable
// source count is the number of unique output keys after reviewed aliases and
// the declared last-wins duplicate policy. Raw occurrences are retained as a
// separate invariant: occurrences = comparable fields + duplicate occurrences.
func TestSampleLogCorpusFieldAccounting(t *testing.T) {
	contracts := loadSampleKeyContracts(t)
	inputs := loadFieldAccountingInputs(t)
	if len(inputs) != loadSampleManifest(t).SampleLogRecordCount {
		t.Fatalf("field-accounting corpus has %d records, want manifest total %d", len(inputs), loadSampleManifest(t).SampleLogRecordCount)
	}

	report := fieldAccountingReport{
		SchemaVersion: 1,
		Verdict:       "pass",
		CountSemantics: fieldAccountingSemantics{
			RawOccurrences: "fixed-layout source fields plus every independently recognized top-level key/value occurrence",
			ComparableRaw:  "unique generated key names after reviewed aliases and last-wins duplicate normalization",
			GeneratedJSON:  "top-level members after marshaling and decoding the EventPayload JSON object",
			DuplicateKeys:  "last-wins; repeats remain explicit in duplicate_keys and duplicate_occurrences and are never counted as additional JSON object members",
		},
		KeyInventory: fieldAccountingKeys{
			SourceMember:       contracts.SourceMember,
			SourceSHA256:       "694773c8af6291ac9decc3b9c271de58995e5b7683bb21dad3ac20443e3406a6",
			DeclaredFamilyKeys: contracts.TotalKeys,
			CoveredFamilyKeys:  contracts.TotalKeys,
		},
		Records: make([]fieldAccountingRecord, 0, len(inputs)),
	}
	for _, extension := range contracts.ReviewedExtensions {
		report.KeyInventory.ReviewedExtensionKeys += len(extension.Keys)
	}

	allowed := sampleAllowedSourceKeys(contracts)
	executors := map[string]*parserpolicy.ParserPackExecutor{}
	packs := map[string]*parserpolicy.ParserPack{}
	families := map[string]*fieldAccountingFamily{}
	services := map[string]string{
		"IMAP": "imap", "POP": "pop", "WEBTOP/WEBMAIL": "webtop", "MTA": "mta",
		"PSS/PABD": "pabd", "MSS/CAPD": "capd", "SMTP-IN": "smtp", "SMTP-OUT": "smtp",
	}

	for _, input := range inputs {
		pack := packs[input.Family]
		if pack == nil {
			pack = buildSampleReviewedProfile(t, contracts, input.Family, sampleFamilySlug(input.Family))
			packs[input.Family] = pack
			executor, err := parserpolicy.NewParserPackExecutor(pack)
			if err != nil {
				t.Fatalf("build %s executor: %v", input.Family, err)
			}
			executors[input.Family] = executor
		}

		record := accountSampleRecord(input, pack, allowed[input.Family])
		disposition, expectedQuarantine := expectedFieldAccountingQuarantine[fmt.Sprintf("%s:%d", input.Corpus, input.SourceLine)]
		record.ExpectedDisposition = "parsed"
		if expectedQuarantine {
			record.ExpectedDisposition = "quarantined"
			record.DispositionReason = disposition.Reason
			if record.RecordSHA256 != disposition.SHA256 {
				record.Errors = append(record.Errors, "expected_quarantine_fixture_hash_mismatch")
			}
		}
		execution, err := executors[input.Family].Execute(&parserpolicy.ExecutionRequest{
			Input: input.Raw, Service: services[input.Family], SourceType: "file", Context: context.Background(),
		})
		if err != nil {
			record.Errors = append(record.Errors, "execution_error:"+err.Error())
		} else if execution.Quarantined || execution.EventPayload == nil {
			record.ParseStatus = "quarantined"
			if execution.ParseTelemetry != nil {
				record.Errors = append(record.Errors, "quarantined:"+strings.Join(execution.ParseTelemetry.ErrorCodes, ","))
			} else {
				record.Errors = append(record.Errors, "quarantined:missing_telemetry")
			}
		} else {
			record.ParseStatus = "parsed"
			payloadJSON, marshalErr := json.Marshal(execution.EventPayload)
			if marshalErr != nil {
				record.Errors = append(record.Errors, "payload_json:"+marshalErr.Error())
			} else {
				var members map[string]json.RawMessage
				if decodeErr := json.Unmarshal(payloadJSON, &members); decodeErr != nil {
					record.Errors = append(record.Errors, "payload_json_decode:"+decodeErr.Error())
				} else {
					record.GeneratedJSONFields = len(members)
				}
			}
			if execution.ParseTelemetry == nil {
				record.Errors = append(record.Errors, "missing_parse_telemetry")
			} else {
				record.TelemetryOutputFields = execution.ParseTelemetry.OutputFieldCount
			}
			if record.GeneratedJSONFields != record.ComparableRawFields {
				record.Errors = append(record.Errors, fmt.Sprintf("field_count_mismatch:raw=%d,json=%d", record.ComparableRawFields, record.GeneratedJSONFields))
			}
			if record.TelemetryOutputFields != record.GeneratedJSONFields {
				record.Errors = append(record.Errors, fmt.Sprintf("telemetry_count_mismatch:telemetry=%d,json=%d", record.TelemetryOutputFields, record.GeneratedJSONFields))
			}
		}
		if record.RawFieldOccurrences != record.ComparableRawFields+record.DuplicateOccurrences {
			record.Errors = append(record.Errors, "duplicate_accounting_mismatch")
		}
		if len(record.UnreviewedKeys) > 0 {
			record.Errors = append(record.Errors, "unreviewed_source_keys:"+strings.Join(record.UnreviewedKeys, ","))
		}
		if expectedQuarantine {
			record.Pass = record.ParseStatus == "quarantined" && record.RecordSHA256 == disposition.SHA256 && errorsContainCode(record.Errors, disposition.Code)
			if !record.Pass {
				record.Errors = append(record.Errors, "expected_quarantine_disposition_mismatch")
			}
		} else {
			record.Pass = record.ParseStatus == "parsed" && len(record.Errors) == 0
		}
		report.Records = append(report.Records, record)
		addFieldAccountingTotals(&report.Totals, record)
		family := families[input.Family]
		if family == nil {
			family = &fieldAccountingFamily{Family: input.Family}
			families[input.Family] = family
		}
		addFieldAccountingFamily(family, record)
	}

	for _, spec := range sampleFamilySpecs {
		if family := families[spec.Name]; family != nil {
			report.Families = append(report.Families, *family)
		}
	}
	if report.Totals.FailingRecords > 0 {
		report.Verdict = "fail"
	}
	writeFieldAccountingReport(t, report)
	if report.Verdict != "pass" {
		t.Fatalf("sample field-accounting gate failed: parsed=%d/%d passing=%d failing=%d quarantined=%d (set %s for full JSON diagnostics)",
			report.Totals.ParsedRecords, report.Totals.Records, report.Totals.PassingRecords,
			report.Totals.FailingRecords, report.Totals.QuarantinedRecords, sampleFieldAccountingReportEnv)
	}
}

func errorsContainCode(errors []string, code string) bool {
	for _, value := range errors {
		if strings.Contains(value, code) {
			return true
		}
	}
	return false
}

// This synthetic record keeps duplicate handling under test even when the
// supplied corpus happens not to repeat a key within an individual record.
func TestSampleFieldAccountingDuplicateSemantics(t *testing.T) {
	contracts := loadSampleKeyContracts(t)
	pack := buildSampleReviewedProfile(t, contracts, "IMAP", "imap")
	raw := "20260101 000000000+0000 node imap 1 0 2 Note;Duplicate(1/1) user=first:user=last"
	record := accountSampleRecord(fieldAccountingInput{
		Corpus: "synthetic-duplicate-contract", SourceLine: 1, Family: "IMAP", Raw: raw,
	}, pack, sampleAllowedSourceKeys(contracts)["IMAP"])
	if record.RawFieldOccurrences != 12 || record.ComparableRawFields != 11 || record.DuplicateOccurrences != 1 || record.DuplicateKeys["user"] != 2 {
		t.Fatalf("duplicate source accounting is not explicit: %+v", record)
	}
	executor, err := parserpolicy.NewParserPackExecutor(pack)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executor.Execute(&parserpolicy.ExecutionRequest{Input: raw, Service: "imap", SourceType: "file", Context: context.Background()})
	if err != nil || execution.Quarantined || execution.EventPayload == nil {
		t.Fatalf("duplicate contract execution failed: result=%+v err=%v", execution, err)
	}
	if execution.EventPayload.FieldCount() != record.ComparableRawFields || execution.EventPayload.Fields["user"] != "last" {
		t.Fatalf("last-wins duplicate contract was not preserved: fields=%#v accounting=%+v", execution.EventPayload.Fields, record)
	}
}

func accountSampleRecord(input fieldAccountingInput, pack *parserpolicy.ParserPack, allowed map[string]bool) fieldAccountingRecord {
	record := fieldAccountingRecord{
		Corpus: input.Corpus, SourceLine: input.SourceLine, Family: input.Family,
		RecordSHA256: fieldAccountingSHA256(input.Raw), ParseStatus: "error", DuplicatePolicy: "last",
		DuplicateKeys: map[string]int{}, Errors: []string{},
	}
	tail, webtop, err := sampleAssignmentTail(input.Raw, input.Family)
	if err != nil {
		record.Errors = append(record.Errors, "raw_layout:"+err.Error())
		return record
	}
	record.RawStructuralFields = len(familyHeaderFields(fieldAccountingCanonicalFamily(input.Family)))
	occurrences := scanSampleTopLevelHeaderOccurrences(tail, webtop)
	record.RawKVOccurrences = len(occurrences)
	outputKeys := map[string]bool{}
	for _, key := range familyHeaderFields(fieldAccountingCanonicalFamily(input.Family)) {
		outputKeys[key] = true
	}
	sourceCounts := map[string]int{}
	unknown := map[string]bool{}
	for _, key := range occurrences {
		sourceCounts[key]++
		if !allowed[key] {
			unknown[key] = true
		}
		outputKeys[sampleProfileOutputKey(pack, key)] = true
	}
	for key, count := range sourceCounts {
		if count > 1 {
			record.DuplicateKeys[key] = count
			record.DuplicateOccurrences += count - 1
		}
	}
	for key := range unknown {
		record.UnreviewedKeys = append(record.UnreviewedKeys, key)
	}
	sort.Strings(record.UnreviewedKeys)
	if len(record.DuplicateKeys) == 0 {
		record.DuplicateKeys = nil
	}
	record.RawFieldOccurrences = record.RawStructuralFields + record.RawKVOccurrences
	record.ComparableRawFields = len(outputKeys)
	return record
}

func scanSampleTopLevelHeaderOccurrences(input string, whitespacePairs bool) []string {
	result := []string{}
	var quote byte
	stack := make([]byte, 0, 4)
	pairs := map[byte]byte{'[': ']', '{': '}', '(': ')'}
	for index := 0; index < len(input); {
		char := input[index]
		if char == '\\' {
			index += 2
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			index++
			continue
		}
		if char == '\'' || char == '"' {
			previous := byte(0)
			if index > 0 {
				previous = input[index-1]
			}
			if index == 0 || previous == '=' || previous == ';' || previous == ':' || previous == '|' || previous == ' ' || previous == '\t' {
				quote = char
			}
			index++
			continue
		}
		if close, opens := pairs[char]; opens {
			stack = append(stack, close)
			index++
			continue
		}
		if len(stack) > 0 && char == stack[len(stack)-1] {
			stack = stack[:len(stack)-1]
			index++
			continue
		}
		candidate := index == 0
		if !candidate && len(stack) == 0 {
			previous := input[index-1]
			candidate = previous == ':' || previous == '|' || (whitespacePairs && (previous == ' ' || previous == '\t'))
		}
		if candidate && len(stack) == 0 {
			start := index
			for index < len(input) && fieldAccountingKeyByte(input[index]) {
				index++
			}
			if index > start && index < len(input) && (input[index] == '=' || input[index] == ';') && fieldAccountingKeyStart(input[start]) {
				result = append(result, input[start:index])
			}
			if index > start {
				continue
			}
		}
		index++
	}
	return result
}

func loadFieldAccountingInputs(t *testing.T) []fieldAccountingInput {
	t.Helper()
	members := loadSampleArchiveMembers(t)
	archiveLines, err := splitSampleCRLFLines(members[sampleLogMember], sampleMaxLines, sampleMaxLineBytes)
	if err != nil {
		t.Fatal(err)
	}
	split, err := splitSampleFamilies(archiveLines)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]fieldAccountingInput, 0, loadSampleManifest(t).SampleLogRecordCount)
	for line, family := range split.LineFamily {
		inputs = append(inputs, fieldAccountingInput{
			Corpus: sampleLogMember, SourceLine: line, Family: family,
			Raw: string(bytes.TrimSuffix(archiveLines[line-1], []byte("\r\n"))),
		})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].SourceLine < inputs[j].SourceLine })

	standalonePath := sampleCorpusPath("source", "1st_test_sample_logs.txt")
	standalone := readSampleFile(t, standalonePath, sampleMaxMemberBytes)
	// The supplied standalone fixture intentionally has no line terminator after
	// record 50. Normalize only that terminal boundary in memory so the shared
	// strict CRLF splitter can still enforce every other record boundary.
	if !bytes.HasSuffix(standalone, []byte("\r\n")) {
		if bytes.Contains(standalone, []byte("\n")) && !bytes.Contains(standalone, []byte("\r\n")) {
			t.Fatal("standalone sample uses unsupported LF-only record boundaries")
		}
		standalone = append(append([]byte(nil), standalone...), '\r', '\n')
	}
	standaloneLines, err := splitSampleCRLFLines(standalone, sampleMaxLines, sampleMaxLineBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(standaloneLines) != 50 {
		t.Fatalf("standalone sample has %d records after terminal-boundary normalization, want 50", len(standaloneLines))
	}
	for index, line := range standaloneLines {
		raw := string(bytes.TrimSuffix(line, []byte("\r\n")))
		inputs = append(inputs, fieldAccountingInput{
			Corpus: "1st_test_sample_logs.txt", SourceLine: index + 1,
			Family: classifyStandaloneSampleFamily(t, raw), Raw: raw,
		})
	}
	return inputs
}

func classifyStandaloneSampleFamily(t *testing.T, raw string) string {
	t.Helper()
	if len(raw) >= 5 && raw[4] == '-' {
		return "WEBTOP/WEBMAIL"
	}
	parts := strings.Fields(raw)
	if len(parts) < 4 {
		t.Fatalf("standalone sample has no service field: %q", raw)
	}
	switch parts[3] {
	case "imap", "imapserv":
		return "IMAP"
	case "pop", "popserv":
		return "POP"
	case "mta":
		return "MTA"
	case "pabd", "pss":
		return "PSS/PABD"
	case "capd", "mss":
		return "MSS/CAPD"
	case "smtp":
		if strings.Contains(parts[2], "rgi") {
			return "SMTP-IN"
		}
		if strings.Contains(parts[2], "rgo") {
			return "SMTP-OUT"
		}
	}
	t.Fatalf("standalone sample family is not independently classifiable: service=%q host=%q", parts[3], parts[2])
	return ""
}

func sampleFamilySlug(family string) string {
	for _, spec := range sampleFamilySpecs {
		if spec.Name == family {
			return spec.Slug
		}
	}
	return ""
}

func fieldAccountingCanonicalFamily(family string) string {
	value, _ := canonicalReviewedFamily(family)
	return value
}

func fieldAccountingKeyByte(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') ||
		(value >= '0' && value <= '9') || value == '_' || value == '.' || value == '-'
}

func fieldAccountingKeyStart(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') || value == '_'
}

func fieldAccountingSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func addFieldAccountingTotals(total *fieldAccountingTotals, record fieldAccountingRecord) {
	total.Records++
	if record.Pass {
		total.AccountedRecords++
	}
	if record.ParseStatus == "parsed" {
		total.ParsedRecords++
		total.ParsedRawFields += record.ComparableRawFields
		total.ParsedJSONFields += record.GeneratedJSONFields
		if record.GeneratedJSONFields == record.ComparableRawFields {
			total.ExactParityRecords++
		}
	}
	if record.ParseStatus == "quarantined" {
		total.QuarantinedRecords++
		if record.ExpectedDisposition == "quarantined" && record.Pass {
			total.ExpectedQuarantined++
		} else {
			total.UnexpectedQuarantined++
		}
	}
	if record.Pass {
		total.PassingRecords++
	} else {
		total.FailingRecords++
	}
	total.RawOccurrences += record.RawFieldOccurrences
	total.ComparableRawFields += record.ComparableRawFields
	total.GeneratedJSONFields += record.GeneratedJSONFields
	total.DuplicateOccurrences += record.DuplicateOccurrences
}

func addFieldAccountingFamily(family *fieldAccountingFamily, record fieldAccountingRecord) {
	family.Records++
	if record.Pass {
		family.PassingRecords++
	} else {
		family.FailingRecords++
	}
	family.RawOccurrences += record.RawFieldOccurrences
	family.ComparableRawFields += record.ComparableRawFields
	family.GeneratedJSONFields += record.GeneratedJSONFields
	family.DuplicateOccurrences += record.DuplicateOccurrences
}

func writeFieldAccountingReport(t *testing.T, report fieldAccountingReport) {
	t.Helper()
	summary, err := json.Marshal(struct {
		SchemaVersion int                   `json:"schema_version"`
		Verdict       string                `json:"verdict"`
		KeyInventory  fieldAccountingKeys   `json:"known_key_inventory"`
		Totals        fieldAccountingTotals `json:"totals"`
	}{report.SchemaVersion, report.Verdict, report.KeyInventory, report.Totals})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FIELD_ACCOUNTING_SUMMARY=%s", summary)
	path := strings.TrimSpace(os.Getenv(sampleFieldAccountingReportEnv))
	if path == "" {
		return
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create field-accounting report directory: %v", err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatalf("write field-accounting report: %v", err)
	}
	t.Logf("FIELD_ACCOUNTING_REPORT=%s", path)
}
