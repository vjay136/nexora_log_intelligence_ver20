package migration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/mixedkv"
	parserpolicy "nexora.local/nli-v20/parser-policy"
	staticlayout "nexora.local/nli-v20/static-layout"
)

const (
	sampleArchiveSHA256   = "29ed50f36785fdf0f5693ab2a44d3613348b0898ec078567836286fac1781fa6"
	sampleLogMember       = "dt/All_Dummy_sample_logs.txt"
	sampleKeyMember       = "dt/All_key_names_alone_from_key_value_pairs_for_All_logs.txt"
	sampleMaxArchiveBytes = 64 * 1024
	sampleMaxEntries      = 4
	sampleMaxMemberBytes  = 256 * 1024
	sampleMaxExpanded     = 256 * 1024
	sampleMaxLines        = 1024
	sampleMaxLineBytes    = 2048
)

type sampleBounds struct {
	MaxArchiveBytes  int64 `json:"max_archive_bytes"`
	MaxEntries       int   `json:"max_entries"`
	MaxMemberBytes   int64 `json:"max_member_bytes"`
	MaxExpandedBytes int64 `json:"max_expanded_bytes"`
	MaxLines         int   `json:"max_lines"`
	MaxLineBytes     int   `json:"max_line_bytes"`
}

type sampleManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Provenance    string `json:"provenance"`
	Source        struct {
		Archive       string `json:"archive"`
		ArchiveBytes  int64  `json:"archive_bytes"`
		ArchiveSHA256 string `json:"archive_sha256"`
		Members       []struct {
			Path   string `json:"path"`
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"members"`
	} `json:"source"`
	Bounds     sampleBounds `json:"bounds"`
	Accounting struct {
		SourceBytes   int64 `json:"source_bytes"`
		SourceLines   int   `json:"source_lines"`
		NonblankLines int   `json:"nonblank_lines"`
		HeadingLines  []int `json:"heading_lines"`
		BlankLines    []int `json:"blank_lines"`
		RecordLines   int   `json:"record_lines"`
	} `json:"accounting"`
	Families []struct {
		Name              string `json:"name"`
		Slug              string `json:"slug"`
		Heading           string `json:"heading"`
		HeadingLine       int    `json:"heading_line"`
		Fixture           string `json:"fixture"`
		RecordLines       int    `json:"record_lines"`
		FirstSourceLine   int    `json:"first_source_line"`
		LastSourceLine    int    `json:"last_source_line"`
		SourceLineNumbers []int  `json:"source_line_numbers"`
		Bytes             int64  `json:"bytes"`
		SHA256            string `json:"sha256"`
	} `json:"families"`
	Anomalies struct {
		MalformedRecords []struct {
			SourceLine     int    `json:"source_line"`
			Family         string `json:"family"`
			Classification string `json:"classification"`
			Reason         string `json:"reason"`
		} `json:"malformed_records"`
		AmbiguousRecords []struct {
			Family         string `json:"family"`
			CanonicalLine  int    `json:"canonical_line"`
			DuplicateLines []int  `json:"duplicate_lines"`
			Classification string `json:"classification"`
			Reason         string `json:"reason"`
		} `json:"ambiguous_records"`
		Notes []string `json:"notes"`
	} `json:"anomalies"`
	ExecutionFixtures []struct {
		Path    string `json:"path"`
		Purpose string `json:"purpose"`
	} `json:"execution_fixtures"`
	AdditionalSourceFiles []struct {
		Purpose             string `json:"purpose"`
		Bytes               int64  `json:"bytes"`
		NonblankRecordLines int    `json:"nonblank_record_lines,omitempty"`
		Path                string `json:"path"`
		SHA256              string `json:"sha256"`
	} `json:"additional_source_files"`
	SampleLogRecordCount int `json:"sample_log_record_count"`
}

type sampleKeySet struct {
	Name string   `json:"name"`
	Keys []string `json:"keys"`
}

type sampleRejectedCandidate struct {
	Family         string `json:"family"`
	SourceLine     int    `json:"source_line"`
	Candidate      string `json:"candidate"`
	Classification string `json:"classification"`
	Reason         string `json:"reason"`
}

type sampleKeyContracts struct {
	SchemaVersion      int                       `json:"schema_version"`
	SourceMember       string                    `json:"source_member"`
	Oracle             string                    `json:"oracle"`
	PreambleLine       int                       `json:"preamble_line"`
	TotalKeys          int                       `json:"total_keys"`
	Families           []sampleKeySet            `json:"families"`
	ReviewNotes        []string                  `json:"review_notes"`
	ReviewedExtensions []sampleKeySet            `json:"reviewed_extensions"`
	RejectedCandidates []sampleRejectedCandidate `json:"rejected_candidates"`
}

type sampleRecordContracts struct {
	SchemaVersion int                    `json:"schema_version"`
	SourceMember  string                 `json:"source_member"`
	Oracle        string                 `json:"oracle"`
	Comparison    string                 `json:"comparison"`
	Records       []sampleRecordContract `json:"records"`
}

type sampleRecordContract struct {
	Family          string                 `json:"family"`
	SourceLine      int                    `json:"source_line"`
	Profile         string                 `json:"profile"`
	ExpectedPayload map[string]interface{} `json:"expected_payload"`
	FixtureLine     int                    `json:"fixture_line"`
	Raw             string                 `json:"raw"`
}

type sampleSplit struct {
	Records       map[string][]byte
	SourceLines   map[string][]int
	LineFamily    map[int]string
	HeadingLines  []int
	BlankLines    []int
	NonblankLines int
	RecordLines   int
}

var sampleFamilySpecs = []struct {
	Name    string
	Slug    string
	Heading string
	Count   int
}{
	{"IMAP", "imap", "For IMAP Sample logs pls use below: This is just few lines, like this i have Trillions of log lines", 51},
	{"POP", "pop", "For POP:", 8},
	{"WEBTOP/WEBMAIL", "webtop-webmail", "For WEBTOP / WEBMAIL:", 19},
	{"MTA", "mta", "For MTA:", 18},
	{"PSS/PABD", "pss-pabd", "For Pabd:", 13},
	{"MSS/CAPD", "mss-capd", "For Capd:", 13},
	{"SMTP-IN", "smtp-in", "For SMTP-IN-->critical logs", 424},
	{"SMTP-OUT", "smtp-out", "For SMTP-OUT-->critical logs:", 132},
}

func TestSampleLogCorpusArchiveAndDeterministicSplit(t *testing.T) {
	manifest := loadSampleManifest(t)
	wantBounds := sampleBounds{
		MaxArchiveBytes: sampleMaxArchiveBytes, MaxEntries: sampleMaxEntries,
		MaxMemberBytes: sampleMaxMemberBytes, MaxExpandedBytes: sampleMaxExpanded,
		MaxLines: sampleMaxLines, MaxLineBytes: sampleMaxLineBytes,
	}
	if manifest.SchemaVersion != 1 || manifest.Bounds != wantBounds {
		t.Fatalf("unexpected manifest schema/bounds: schema=%d bounds=%+v", manifest.SchemaVersion, manifest.Bounds)
	}
	if manifest.Source.ArchiveSHA256 != sampleArchiveSHA256 {
		t.Fatalf("manifest archive SHA-256 = %s", manifest.Source.ArchiveSHA256)
	}
	archivePath := safeSampleFixturePath(t, manifest.Source.Archive)
	archive := readSampleFile(t, archivePath, sampleMaxArchiveBytes)
	if int64(len(archive)) != manifest.Source.ArchiveBytes || sha256Hex(archive) != sampleArchiveSHA256 {
		t.Fatalf("source archive provenance mismatch: bytes=%d sha256=%s", len(archive), sha256Hex(archive))
	}
	members, err := readSampleArchive(archive, wantBounds)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("archive file members = %d, want 2", len(members))
	}
	for _, member := range manifest.Source.Members {
		content, exists := members[member.Path]
		if !exists || int64(len(content)) != member.Bytes || sha256Hex(content) != member.SHA256 {
			t.Errorf("archive member provenance mismatch for %q", member.Path)
		}
	}
	logSource := members[sampleLogMember]
	if int64(len(logSource)) != manifest.Accounting.SourceBytes {
		t.Fatalf("log source bytes = %d, want %d", len(logSource), manifest.Accounting.SourceBytes)
	}
	lines, err := splitSampleCRLFLines(logSource, wantBounds.MaxLines, wantBounds.MaxLineBytes)
	if err != nil {
		t.Fatal(err)
	}
	split, err := splitSampleFamilies(lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 708 || split.RecordLines != 678 || split.NonblankLines != 686 {
		t.Fatalf("source accounting: lines=%d nonblank=%d records=%d", len(lines), split.NonblankLines, split.RecordLines)
	}
	if manifest.Accounting.SourceLines != len(lines) || manifest.Accounting.NonblankLines != split.NonblankLines || manifest.Accounting.RecordLines != split.RecordLines ||
		!reflect.DeepEqual(manifest.Accounting.HeadingLines, split.HeadingLines) || !reflect.DeepEqual(manifest.Accounting.BlankLines, split.BlankLines) {
		t.Fatalf("manifest line accounting differs from source")
	}
	if len(manifest.Families) != len(sampleFamilySpecs) {
		t.Fatalf("manifest families = %d", len(manifest.Families))
	}
	for index, expected := range sampleFamilySpecs {
		family := manifest.Families[index]
		if family.Name != expected.Name || family.Slug != expected.Slug || family.Heading != expected.Heading || family.RecordLines != expected.Count {
			t.Fatalf("family[%d] metadata = %+v", index, family)
		}
		actualLines := split.SourceLines[expected.Slug]
		if len(actualLines) != expected.Count || !reflect.DeepEqual(actualLines, family.SourceLineNumbers) {
			t.Fatalf("%s source-line accounting differs", expected.Name)
		}
		if family.FirstSourceLine != actualLines[0] || family.LastSourceLine != actualLines[len(actualLines)-1] {
			t.Fatalf("%s source range differs", expected.Name)
		}
		fixture := readSampleFile(t, safeSampleFixturePath(t, family.Fixture), sampleMaxMemberBytes)
		actual := split.Records[expected.Slug]
		if !bytes.Equal(fixture, actual) || int64(len(actual)) != family.Bytes || sha256Hex(actual) != family.SHA256 {
			t.Fatalf("%s fixture is not the deterministic byte-preserving split", expected.Name)
		}
	}

	wantMalformed := []int{137, 138, 139, 140}
	if len(manifest.Anomalies.MalformedRecords) != len(wantMalformed) {
		t.Fatalf("malformed records = %d", len(manifest.Anomalies.MalformedRecords))
	}
	for index, record := range manifest.Anomalies.MalformedRecords {
		if record.SourceLine != wantMalformed[index] || record.Family != "MSS/CAPD" || record.Classification != "malformed-placeholder" || split.LineFamily[record.SourceLine] != "MSS/CAPD" || record.Reason == "" {
			t.Errorf("malformed record[%d] is not independently identified: %+v", index, record)
		}
		tail := string(bytes.TrimSuffix(lines[record.SourceLine-1], []byte("\r\n")))
		assignments := strings.Count(tail[strings.Index(tail, " id"):], "=")
		if (record.SourceLine == 137 && assignments != 1) || (record.SourceLine != 137 && assignments != 0) {
			t.Errorf("malformed source line %d assignment count = %d", record.SourceLine, assignments)
		}
	}
	if len(manifest.Anomalies.AmbiguousRecords) != 17 {
		t.Fatalf("ambiguous duplicate groups = %d, want 17", len(manifest.Anomalies.AmbiguousRecords))
	}
	for _, group := range manifest.Anomalies.AmbiguousRecords {
		if group.Classification != "ambiguous-exact-duplicate" || group.Reason == "" || split.LineFamily[group.CanonicalLine] != group.Family {
			t.Errorf("invalid ambiguous record catalog entry: %+v", group)
			continue
		}
		for _, duplicate := range group.DuplicateLines {
			if !bytes.Equal(lines[group.CanonicalLine-1], lines[duplicate-1]) || split.LineFamily[duplicate] != group.Family {
				t.Errorf("cataloged duplicate %d/%d is not byte-identical within %s", group.CanonicalLine, duplicate, group.Family)
			}
		}
	}
}

func TestSampleLogKeyInventoryMatchesIndependentContract(t *testing.T) {
	members := loadSampleArchiveMembers(t)
	contracts := loadSampleKeyContracts(t)
	if contracts.SchemaVersion != 2 || contracts.SourceMember != sampleKeyMember || contracts.TotalKeys != 278 || len(contracts.Families) != 8 || contracts.Oracle == "" || len(contracts.ReviewNotes) < 4 {
		t.Fatalf("invalid expected key contract metadata: %+v", contracts)
	}
	actual, err := parseSampleKeyInventory(members[sampleKeyMember])
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	contractKeys := map[string]map[string]bool{}
	for _, family := range contracts.Families {
		total += len(family.Keys)
		if !reflect.DeepEqual(actual[family.Name], family.Keys) {
			t.Errorf("%s key contract differs\nactual: %#v\nexpected: %#v", family.Name, actual[family.Name], family.Keys)
		}
		contractKeys[family.Name] = map[string]bool{}
		for _, key := range family.Keys {
			if contractKeys[family.Name][key] {
				t.Errorf("%s key contract repeats %q", family.Name, key)
			}
			contractKeys[family.Name][key] = true
		}
	}
	if total != contracts.TotalKeys || len(actual) != len(contracts.Families) {
		t.Fatalf("key accounting: actual families=%d total=%d", len(actual), total)
	}
	if len(contracts.ReviewedExtensions) != len(contracts.Families) || len(contracts.RejectedCandidates) != 11 {
		t.Fatalf("review accounting: extensions=%d rejected=%d", len(contracts.ReviewedExtensions), len(contracts.RejectedCandidates))
	}
	scan, err := scanSampleAssignedKeys(members[sampleLogMember])
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range contracts.ReviewedExtensions {
		for _, key := range family.Keys {
			if contractKeys[family.Name][key] {
				t.Errorf("%s reviewed extension %q duplicates the supplied contract", family.Name, key)
			}
			if !scan.ByFamily[family.Name][key] {
				t.Errorf("%s reviewed extension %q has no top-level assignment evidence", family.Name, key)
			}
		}
	}
	lines, err := splitSampleCRLFLines(members[sampleLogMember], sampleMaxLines, sampleMaxLineBytes)
	if err != nil {
		t.Fatal(err)
	}
	split, err := splitSampleFamilies(lines)
	if err != nil {
		t.Fatal(err)
	}
	for _, rejected := range contracts.RejectedCandidates {
		if rejected.Classification == "" || rejected.Reason == "" || rejected.SourceLine < 1 || rejected.SourceLine > len(lines) || split.LineFamily[rejected.SourceLine] != rejected.Family {
			t.Fatalf("invalid rejected candidate evidence: %+v", rejected)
		}
		raw := string(lines[rejected.SourceLine-1])
		if !strings.Contains(raw, rejected.Candidate) {
			t.Errorf("rejected candidate %q has no source evidence on line %d", rejected.Candidate, rejected.SourceLine)
		}
		if scan.ByLine[rejected.SourceLine][rejected.Candidate] {
			t.Errorf("payload fragment %q on line %d was normalized as a top-level assignment", rejected.Candidate, rejected.SourceLine)
		}
	}
}

func TestSampleLogManualRecordContractsExecuteExactly(t *testing.T) {
	members := loadSampleArchiveMembers(t)
	contracts := loadSampleRecordContracts(t)
	if contracts.SchemaVersion != 1 || contracts.SourceMember != sampleLogMember || len(contracts.Records) != 8 || contracts.Oracle == "" {
		t.Fatalf("invalid record contract metadata")
	}
	sourceLines, err := splitSampleCRLFLines(members[sampleLogMember], sampleMaxLines, sampleMaxLineBytes)
	if err != nil {
		t.Fatal(err)
	}
	familySlug := map[string]string{"IMAP": "imap", "POP": "pop", "WEBTOP/WEBMAIL": "webtop-webmail", "MTA": "mta", "PSS/PABD": "pss-pabd", "MSS/CAPD": "mss-capd", "SMTP-IN": "smtp-in", "SMTP-OUT": "smtp-out"}
	for _, contract := range contracts.Records {
		t.Run(strings.ReplaceAll(contract.Family, "/", "-"), func(t *testing.T) {
			if contract.SourceLine < 1 || contract.SourceLine > len(sourceLines) {
				t.Fatalf("source line %d is outside corpus", contract.SourceLine)
			}
			sourceRaw := string(bytes.TrimSuffix(sourceLines[contract.SourceLine-1], []byte("\r\n")))
			if sourceRaw != contract.Raw {
				t.Fatalf("raw oracle no longer matches source line %d", contract.SourceLine)
			}
			fixtureLines, splitErr := splitSampleCRLFLines(readSampleFile(t, sampleCorpusPath("families", familySlug[contract.Family]+".log"), sampleMaxMemberBytes), sampleMaxLines, sampleMaxLineBytes)
			if splitErr != nil {
				t.Fatal(splitErr)
			}
			if contract.FixtureLine < 1 || contract.FixtureLine > len(fixtureLines) || string(bytes.TrimSuffix(fixtureLines[contract.FixtureLine-1], []byte("\r\n"))) != contract.Raw {
				t.Fatalf("family-relative provenance mismatch at line %d", contract.FixtureLine)
			}
			actual := executeSampleProfile(t, contract.Profile, contract.Raw)
			assertSampleJSONEqual(t, actual, contract.ExpectedPayload)
		})
	}
}

func TestSampleLogAlloyImporterDoesNotClaimUnsupportedParity(t *testing.T) {
	contract := findSampleRecordContract(t, "SMTP-IN")
	alloy := readSampleFile(t, filepath.Join("..", "..", "testdata", "migration", "smtp-in", "smtp-in_universal.alloy"), 64*1024)
	regression := parserpolicy.RegressionCase{
		Name: "sample-corpus-smtp-in-line-276", Input: contract.Raw, Service: "smtp", SourceType: "file",
		ExpectedPayload: contract.ExpectedPayload, ExpectedParser: "*", ExpectedRoute: "normal",
	}
	result, err := TranslateAlloy(alloy, "smtp-in_universal.alloy", regression)
	if err != nil {
		t.Fatal(err)
	}
	if result.ParityStatus != ParityFailed || len(result.RegressionOutcomes) != 1 || result.RegressionOutcomes[0].Passed || result.RegressionOutcomes[0].Error != "generated parser quarantined the sample" {
		t.Fatalf("unsupported Alloy control flow claimed parity: %+v", result.RegressionOutcomes)
	}
	if len(result.Operations) == 0 || len(result.LineMappings) == 0 || result.ParserPack == nil || len(result.UnsupportedConstructs) == 0 {
		t.Fatal("Alloy importer omitted structural provenance, executable source stages, or unsupported semantics")
	}
	for _, operation := range result.Operations {
		if strings.HasPrefix(operation.Kind, "derived.family_parser") {
			t.Fatalf("synthetic family operation escaped into Alloy result: %+v", operation)
		}
	}
}

func TestSampleLogVectorImporterExecutesManualWebtopOracle(t *testing.T) {
	contract := findSampleRecordContract(t, "WEBTOP/WEBMAIL")
	vector := readSampleFile(t, sampleCorpusPath("importers", "webtop-supported.toml"), 16*1024)
	regression := parserpolicy.RegressionCase{
		Name: "sample-corpus-webtop-line-68", Input: contract.Raw, Service: "webtop", SourceType: "file",
		ExpectedPayload: contract.ExpectedPayload, ExpectedParser: "*", ExpectedRoute: "normal",
	}
	result, err := TranslateVector(vector, "webtop-supported.toml", regression)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UnsupportedConstructs) != 0 || result.ParityStatus != ParityPassed || len(result.RegressionOutcomes) != 1 || !result.RegressionOutcomes[0].Passed {
		t.Fatalf("Vector importer/executor did not match independent oracle: unsupported=%+v outcomes=%+v", result.UnsupportedConstructs, result.RegressionOutcomes)
	}
	translated := map[string]bool{}
	for _, operation := range result.Operations {
		if operation.Status == "translated" {
			translated[operation.Kind] = true
		}
	}
	for _, kind := range []string{"vrl.parse_regex", "vrl.parse_key_value", "vrl.del"} {
		if !translated[kind] {
			t.Errorf("Vector operation %s was not translated", kind)
		}
	}
}

func TestSampleLogComplexSourceSemanticsAreLocalized(t *testing.T) {
	tests := []struct {
		name, relative, format string
	}{
		{"vector-imap", "imap/imap_parse.toml", "vector"},
		{"vector-pop", "pop/pop_parse.toml", "vector"},
		{"alloy-pabd", "pabd-pss/pabd_processor.alloy", "alloy"},
		{"alloy-capd", "capd-mss/capd_processor.alloy", "alloy"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := readSampleFile(t, filepath.Join("..", "..", "testdata", "migration", filepath.FromSlash(test.relative)), 64*1024)
			result := translateSampleConfig(t, content, filepath.Base(test.relative), test.format)
			if !result.Parsed || !result.Success || result.ParserPack == nil || len(result.UnsupportedConstructs) == 0 {
				t.Fatalf("complex source accounting is incomplete: pack=%v unsupported=%+v", result.ParserPack != nil, result.UnsupportedConstructs)
			}
			for _, unsupported := range result.UnsupportedConstructs {
				if unsupported.Type == "" || unsupported.Description == "" || unsupported.Repair == "" || unsupported.Severity == "" {
					t.Fatalf("unsupported construct lacks explicit evidence: %+v", unsupported)
				}
			}
			translatedStages := map[string]bool{}
			for _, operation := range result.Operations {
				if strings.HasPrefix(operation.Kind, "derived.family_parser") {
					t.Fatalf("filename/process fallback escaped into importer operations: %+v", operation)
				}
				if operation.Status == "translated" && operation.IRStageID != "" {
					if operation.Range.Start.Line < 1 || operation.Range.End.Line < operation.Range.Start.Line {
						t.Fatalf("translated stage lacks source range: %+v", operation)
					}
					translatedStages[operation.IRRuleID+"/"+operation.IRStageID] = true
				}
			}
			for _, rule := range result.ParserPack.Rules {
				for _, stage := range rule.Stages {
					if !translatedStages[rule.ID+"/"+stage.ID] {
						t.Errorf("generated stage %s/%s has no translated source construct", rule.ID, stage.ID)
					}
				}
			}
		})
	}
}

func TestImportersNeverCreateFilenameOnlyFamilyRules(t *testing.T) {
	alloy, err := TranslateAlloy([]byte("loki.process \"imap\" {\n}\n"), "imap_processor.alloy")
	if err != nil {
		t.Fatal(err)
	}
	vector, err := TranslateVector([]byte("[sources.input]\ntype = \"file\"\ninclude = [\"sample.log\"]\n"), "imap_parse.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*MigrationResult{alloy, vector} {
		if result.ParserPack != nil {
			t.Fatalf("%s created a parser pack without source-declared parser stages", result.SourceFile)
		}
		for _, operation := range result.Operations {
			if operation.Status == "translated" || strings.HasPrefix(operation.Kind, "derived.family_parser") {
				t.Fatalf("%s falsely translated filename-only semantics: %+v", result.SourceFile, operation)
			}
		}
	}
}

func TestSampleLogSMTPOutRepairProvenance(t *testing.T) {
	var repair struct {
		SchemaVersion       int    `json:"schema_version"`
		SourceFile          string `json:"source_file"`
		OriginalSHA256      string `json:"original_sha256"`
		RepairedSHA256      string `json:"repaired_sha256"`
		Repair              string `json:"repair"`
		RemovedOriginalLine int    `json:"removed_original_line"`
		RemovedBytesHex     string `json:"removed_bytes_hex"`
		Rationale           string `json:"rationale"`
	}
	repairPath := filepath.Join("..", "..", "testdata", "migration", "smtp-out", "smtp-out_processor.repair.json")
	decodeSampleJSON(t, repairPath, 4096, &repair)
	if repair.SchemaVersion != 1 || repair.SourceFile != "smtp-out_processor.alloy" || repair.OriginalSHA256 != "5daaf08766af0a6dc4327db79bdf689dd7df5a7fd9bdf35fb8233fa869b43332" ||
		repair.RemovedOriginalLine != 554 || repair.RemovedBytesHex != "7d0d0a" || repair.Repair == "" || repair.Rationale == "" {
		t.Fatalf("invalid SMTP-OUT repair provenance: %+v", repair)
	}
	content := readSampleFile(t, filepath.Join("..", "..", "testdata", "migration", "smtp-out", repair.SourceFile), 64*1024)
	if sha256Hex(content) != repair.RepairedSHA256 || repair.RepairedSHA256 != "24ab85c72bb752a0d39c5be9d0096ffd0b8dd1ec2c6e30fa61af5dd2b8adc04f" {
		t.Fatalf("SMTP-OUT repaired hash mismatch")
	}
	result := translateSampleConfig(t, content, repair.SourceFile, "alloy")
	if !result.Parsed || !result.Success || result.ParserPack == nil {
		t.Fatalf("repaired SMTP-OUT source did not translate: %+v", result.Errors)
	}
}

func TestSampleLogAll678RecordsExecuteWithBoundedKeyContracts(t *testing.T) {
	contracts := loadSampleKeyContracts(t)
	allowedSource := sampleAllowedSourceKeys(contracts)
	scan, err := scanSampleAssignedKeys(loadSampleArchiveMembers(t)[sampleLogMember])
	if err != nil {
		t.Fatal(err)
	}
	services := map[string]string{
		"IMAP": "imap", "POP": "pop", "WEBTOP/WEBMAIL": "webtop", "MTA": "mta",
		"PSS/PABD": "pabd", "MSS/CAPD": "capd", "SMTP-IN": "smtp", "SMTP-OUT": "smtp",
	}
	structural := map[string]bool{
		"date": true, "time": true, "hostname": true, "service": true,
		"major_pid": true, "pid_status": true, "minor_pid": true,
		"event_type": true, "event": true, "code": true,
	}
	manifest := loadSampleManifest(t)
	familyManifest := map[string]struct {
		fixture     string
		sourceLines []int
	}{}
	for _, family := range manifest.Families {
		familyManifest[family.Name] = struct {
			fixture     string
			sourceLines []int
		}{family.Fixture, family.SourceLineNumbers}
	}
	malformed := map[int]bool{137: true, 138: true, 139: true, 140: true}
	validCount, quarantinedCount, profileCount := 0, 0, 0
	requiredAssignments, sourceAliases, envelopeAliases := 0, 0, 0
	for _, family := range sampleFamilySpecs {
		pack := buildSampleReviewedProfile(t, contracts, family.Name, family.Slug)
		if pack.SourceType != "manual" || pack.ReviewState != parserpolicy.ReviewStatePending || pack.Metadata["profile_provenance"] != "explicit-contract-not-importer-derived" || pack.Metadata["importer_parity"] != "not-applicable" {
			t.Fatalf("%s profile provenance is misleading: %+v", family.Name, pack.Metadata)
		}
		profileCount++
		executor, err := parserpolicy.NewParserPackExecutor(pack)
		if err != nil {
			t.Fatalf("%s executor: %v", family.Name, err)
		}
		allowedOutput := map[string]bool{}
		for key := range allowedSource[family.Name] {
			allowedOutput[sampleProfileOutputKey(pack, key)] = true
		}
		fixture := familyManifest[family.Name]
		lines, err := splitSampleCRLFLines(readSampleFile(t, safeSampleFixturePath(t, fixture.fixture), sampleMaxMemberBytes), sampleMaxLines, sampleMaxLineBytes)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != len(fixture.sourceLines) {
			t.Fatalf("%s fixture/source map mismatch", family.Name)
		}
		for index, line := range lines {
			raw := string(bytes.TrimSuffix(line, []byte("\r\n")))
			sourceLine := fixture.sourceLines[index]
			execution, executeErr := executor.Execute(&parserpolicy.ExecutionRequest{Input: raw, Service: services[family.Name], SourceType: "file", Context: context.Background()})
			if executeErr != nil {
				t.Fatalf("%s source line %d execution: %v", family.Name, sourceLine, executeErr)
			}
			if malformed[sourceLine] {
				quarantinedCount++
				if !execution.Quarantined || execution.EventPayload != nil || execution.ParseTelemetry == nil || len(execution.ParseTelemetry.ErrorCodes) == 0 {
					t.Errorf("%s malformed source line %d lacked quarantine diagnostics", family.Name, sourceLine)
				}
				continue
			}
			validCount++
			if execution.Quarantined || execution.EventPayload == nil || execution.EventPayload.FieldCount() == 0 || execution.EventPayload.FieldCount() > 512 {
				t.Errorf("%s valid source line %d did not produce a bounded payload: %+v", family.Name, sourceLine, execution.ParseTelemetry)
				continue
			}
			if !strings.Contains(execution.RuleID, "reviewed_") {
				t.Errorf("%s source line %d bypassed the reviewed profile: %s", family.Name, sourceLine, execution.RuleID)
			}
			for key := range execution.EventPayload.Fields {
				if !structural[key] && !allowedOutput[key] {
					t.Errorf("%s source line %d emitted undocumented key %q", family.Name, sourceLine, key)
				}
			}
			for key := range scan.ByLine[sourceLine] {
				if !allowedSource[family.Name][key] {
					continue
				}
				requiredAssignments++
				output := sampleProfileOutputKey(pack, key)
				if _, exists := execution.EventPayload.Fields[output]; !exists {
					t.Errorf("%s source line %d missed required assignment %q (output %q)", family.Name, sourceLine, key, output)
				}
			}
			for _, collision := range []struct {
				key     string
				counter *int
			}{{"source", &sourceAliases}, {"envelope", &envelopeAliases}} {
				expected, found := sampleSimpleAssignmentValue(raw, collision.key)
				if !found {
					continue
				}
				(*collision.counter)++
				output := "input." + collision.key
				if actual := execution.EventPayload.Fields[output]; actual != expected {
					t.Errorf("%s source line %d %s=%#v, want %#v", family.Name, sourceLine, output, actual, expected)
				}
			}
			if _, exists := execution.EventPayload.Fields["source"]; exists {
				t.Errorf("%s source line %d emitted forbidden exact source key", family.Name, sourceLine)
			}
			if _, exists := execution.EventPayload.Fields["envelope"]; exists {
				t.Errorf("%s source line %d emitted forbidden exact envelope key", family.Name, sourceLine)
			}
		}
	}
	t.Logf("coverage: valid=%d quarantined=%d profiles=%d required-line-keys=%d source-aliases=%d envelope-aliases=%d", validCount, quarantinedCount, profileCount, requiredAssignments, sourceAliases, envelopeAliases)
	if validCount != 674 || quarantinedCount != 4 || profileCount != 8 || sourceAliases != 7 || envelopeAliases != 5 || requiredAssignments != 5691 {
		t.Fatalf("corpus execution: valid=%d quarantined=%d profiles=%d required=%d source_aliases=%d envelope_aliases=%d", validCount, quarantinedCount, profileCount, requiredAssignments, sourceAliases, envelopeAliases)
	}
}

func translateSampleConfig(t *testing.T, content []byte, name, format string) *MigrationResult {
	t.Helper()
	var result *MigrationResult
	var err error
	if format == "vector" {
		result, err = TranslateVector(content, name)
	} else {
		result, err = TranslateAlloy(content, name)
	}
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSampleLogV3IndependentGrammarOracles(t *testing.T) {
	var oracles struct {
		SchemaVersion   int      `json:"schema_version"`
		Oracle          string   `json:"oracle"`
		GrammarCoverage []string `json:"grammar_coverage"`
		Records         []struct {
			Family         string                 `json:"family"`
			SourceLine     int                    `json:"source_line"`
			Description    string                 `json:"description"`
			ExpectedFields map[string]interface{} `json:"expected_fields"`
			AbsentFields   []string               `json:"absent_fields,omitempty"`
		} `json:"records"`
	}
	decodeSampleJSON(t, sampleCorpusPath("records.v2.expected.json"), 64*1024, &oracles)
	if oracles.SchemaVersion != 3 || oracles.Oracle == "" || len(oracles.GrammarCoverage) < 10 || len(oracles.Records) != 37 {
		t.Fatalf("invalid v3 oracle coverage metadata")
	}
	services := map[string]string{
		"IMAP": "imap", "POP": "pop", "WEBTOP/WEBMAIL": "webtop", "MTA": "mta",
		"PSS/PABD": "pabd", "MSS/CAPD": "capd", "SMTP-IN": "smtp", "SMTP-OUT": "smtp",
	}
	members := loadSampleArchiveMembers(t)
	lines, err := splitSampleCRLFLines(members[sampleLogMember], sampleMaxLines, sampleMaxLineBytes)
	if err != nil {
		t.Fatal(err)
	}
	split, err := splitSampleFamilies(lines)
	if err != nil {
		t.Fatal(err)
	}
	contracts := loadSampleKeyContracts(t)
	executors := map[string]*parserpolicy.ParserPackExecutor{}
	familyCounts := map[string]int{}
	seenLines := map[int]bool{}
	for _, oracle := range oracles.Records {
		if oracle.Description == "" || len(oracle.ExpectedFields) == 0 || seenLines[oracle.SourceLine] || split.LineFamily[oracle.SourceLine] != oracle.Family {
			t.Fatalf("invalid oracle provenance for %s source line %d", oracle.Family, oracle.SourceLine)
		}
		seenLines[oracle.SourceLine] = true
		familyCounts[oracle.Family]++
		executor := executors[oracle.Family]
		if executor == nil {
			slug := ""
			for _, family := range sampleFamilySpecs {
				if family.Name == oracle.Family {
					slug = family.Slug
				}
			}
			pack := buildSampleReviewedProfile(t, contracts, oracle.Family, slug)
			executor, err = parserpolicy.NewParserPackExecutor(pack)
			if err != nil {
				t.Fatal(err)
			}
			executors[oracle.Family] = executor
		}
		raw := string(bytes.TrimSuffix(lines[oracle.SourceLine-1], []byte("\r\n")))
		execution, err := executor.Execute(&parserpolicy.ExecutionRequest{Input: raw, Service: services[oracle.Family], SourceType: "file", Context: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		if execution.Quarantined || execution.EventPayload == nil {
			t.Fatalf("oracle %s source line %d quarantined", oracle.Family, oracle.SourceLine)
		}
		for field, expected := range oracle.ExpectedFields {
			actual, exists := execution.EventPayload.Fields[field]
			if !exists {
				t.Errorf("%s source line %d missing oracle field %q", oracle.Family, oracle.SourceLine, field)
				continue
			}
			assertSampleJSONEqual(t, actual, expected)
		}
		for _, field := range oracle.AbsentFields {
			if _, exists := execution.EventPayload.Fields[field]; exists {
				t.Errorf("%s source line %d unexpectedly emitted field %q", oracle.Family, oracle.SourceLine, field)
			}
		}
	}
	for _, family := range sampleFamilySpecs {
		if familyCounts[family.Name] < 3 {
			t.Errorf("%s has only %d independent v3 oracles", family.Name, familyCounts[family.Name])
		}
	}
}

func TestSampleLogFutureKeysAndConfigurableDelimiters(t *testing.T) {
	config := mixedkv.DefaultConfig()
	config.PairSeparators = []string{"<|>"}
	config.AssignmentSeparators = []string{"=>"}
	config.KeyAllowPatterns = []string{`^(future\.trace-id|vendor-new-key|metric\.v2)$`}
	config.ValueTypes = map[string]string{"metric.v2": "integer"}
	config.MaxInputBytes = 128
	config.MaxFields = 8
	input := `future.trace-id=>abc-123<|>vendor-new-key=>enabled<|>metric.v2=>7`
	result, err := mixedkv.Parse(input, config)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]interface{}{"future.trace-id": "abc-123", "vendor-new-key": "enabled", "metric.v2": int64(7)}
	if !reflect.DeepEqual(result.Fields, expected) || len(result.Entries) != 3 {
		t.Fatalf("future key/delimiter contract failed: %#v", result)
	}

	limited := config
	limited.MaxInputBytes = 16
	_, err = mixedkv.Parse(input, limited)
	var parseErr *mixedkv.Error
	if !errors.As(err, &parseErr) || parseErr.Code != mixedkv.CodeInputTooLarge {
		t.Fatalf("custom parser input bound was not explicit: %v", err)
	}
}

func TestReviewedFamilyProfileAdditionalKeysAreExplicit(t *testing.T) {
	base := FamilyProfileSpec{
		ID: "future_key_profile", Family: "smtp", ContractKeys: []string{"id"},
		ReviewReference: "future-key-review-1",
	}
	build := func(spec FamilyProfileSpec) map[string]interface{} {
		pack, err := BuildReviewedFamilyProfile(spec)
		if err != nil {
			t.Fatal(err)
		}
		if pack.SourceType != "manual" || pack.ReviewState != parserpolicy.ReviewStatePending {
			t.Fatalf("future-key profile has misleading provenance: source=%s review=%s", pack.SourceType, pack.ReviewState)
		}
		executor, err := parserpolicy.NewParserPackExecutor(pack)
		if err != nil {
			t.Fatal(err)
		}
		raw := "20260101 000000000+0000 node smtp 1 0 2 Note;FutureKey(1/1) id=abc:vendor-new-key=enabled"
		result, err := executor.Execute(&parserpolicy.ExecutionRequest{Input: raw, Service: "smtp", SourceType: "file", Context: context.Background()})
		if err != nil || result.Quarantined || result.EventPayload == nil {
			t.Fatalf("future-key profile execution failed: result=%+v err=%v", result, err)
		}
		return result.EventPayload.Fields
	}
	without := build(base)
	if _, exists := without["vendor-new-key"]; exists {
		t.Fatal("unreviewed future key was extracted")
	}
	base.AdditionalReviewedKeys = []string{"vendor-new-key"}
	with := build(base)
	if with["vendor-new-key"] != "enabled" {
		t.Fatalf("explicit future key was not extracted: %#v", with)
	}
}

func TestSampleLogArchiveGuardsRejectTraversalAndExpansion(t *testing.T) {
	bounds := sampleBounds{MaxArchiveBytes: 4096, MaxEntries: 2, MaxMemberBytes: 8, MaxExpandedBytes: 8, MaxLines: 2, MaxLineBytes: 8}
	if _, err := readSampleArchive(makeSampleZIP(t, "../escape.txt", []byte("x")), bounds); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("archive traversal was accepted: %v", err)
	}
	if _, err := readSampleArchive(makeSampleZIP(t, "safe.txt", []byte("123456789")), bounds); err == nil || !strings.Contains(err.Error(), "member limit") {
		t.Fatalf("archive expansion was accepted: %v", err)
	}
}

func executeSampleProfile(t *testing.T, profile, input string) map[string]interface{} {
	t.Helper()
	switch profile {
	case "native_colon_kv":
		parser, err := staticlayout.NewSMTPParser()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		return mergeSampleKV(t, parsed.Fields, ":")
	case "webtop_logfmt":
		parser, err := staticlayout.NewParser(staticlayout.Config{
			MaxInputBytes: sampleMaxLineBytes, FailOnMissing: true,
			Tokens: []staticlayout.TokenSpec{
				{Index: 0, Name: "date", Type: staticlayout.TokenTypeString, Required: true},
				{Index: 1, Name: "time", Type: staticlayout.TokenTypeString, Required: true},
				{Index: 2, Name: "_kv_tail", Type: staticlayout.TokenTypeRestOfLine, Required: true},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		return mergeSampleKV(t, parsed.Fields, " ", "\t")
	case "pabd_regex", "capd_regex":
		return executeSampleRegexProfile(t, profile, input)
	default:
		t.Fatalf("unsupported sample profile %q", profile)
		return nil
	}
}

func mergeSampleKV(t *testing.T, input map[string]interface{}, pairSeparators ...string) map[string]interface{} {
	t.Helper()
	fields := make(map[string]interface{}, len(input))
	for key, value := range input {
		fields[key] = value
	}
	tail, ok := fields["_kv_tail"].(string)
	if !ok {
		t.Fatal("static parser did not preserve a KV tail")
	}
	delete(fields, "_kv_tail")
	config := mixedkv.DefaultConfig()
	config.PairSeparators = pairSeparators
	config.AssignmentSeparators = []string{"="}
	config.DuplicatePolicy = mixedkv.DuplicateLast
	config.MaxInputBytes = sampleMaxLineBytes
	config.MaxFields = 128
	config.MaxValueBytes = sampleMaxLineBytes
	parsed, err := mixedkv.Parse(tail, config)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range parsed.Fields {
		if _, collision := fields[key]; collision {
			t.Fatalf("sample parser field collision on %q", key)
		}
		fields[key] = value
	}
	return fields
}

func executeSampleRegexProfile(t *testing.T, profile, input string) map[string]interface{} {
	t.Helper()
	var service, pattern string
	groups := map[string]string{}
	switch profile {
	case "pabd_regex":
		service = "pabd"
		pattern = `^(?P<date>\d{8}) (?P<time>\d{9}[+-]\d{4}) (?P<hostname>\S+) (?P<service>\S+) (?P<major_pid>\d+) (?P<pid_status>\d+) (?P<minor_pid>\d+) (?P<event_type>[A-Za-z]+);(?P<event>CARDDAV) id=(?P<id>[^:]+):(?P<request_type>[^:]+):method=(?P<method>[^:]+):realm=(?P<realm>[^:]+):address=(?P<address>[^:]+)$`
	case "capd_regex":
		service = "capd"
		pattern = `^(?P<date>\d{8}) (?P<time>\d{9}[+-]\d{4}) (?P<hostname>\S+) (?P<service>\S+) (?P<major_pid>\d+) (?P<pid_status>\d+) (?P<minor_pid>\d+) (?P<event_type>[A-Za-z]+);(?P<event>CalDavResponse) id=(?P<id>[^:]+):username=(?P<username>[^:]+):cosId=(?P<cosId>[^:]+):auth=(?P<auth>[^:]+):request=(?P<request>[^:]+):depth=(?P<depth>[^:]+):user-agent=(?P<user_agent>[^:]+):error_id=(?P<error_id>[^:]+):http_code=(?P<http_code>[^:]+):status=(?P<status>[^:]+):resp_time=(?P<resp_time>[^:]+):resp_size=(?P<resp_size>[^:]+)$`
		groups["user_agent"] = "user-agent"
	default:
		t.Fatalf("unsupported regex profile %q", profile)
	}
	pack := &parserpolicy.ParserPack{
		ID: "sample_" + strings.TrimSuffix(profile, "_regex"), Version: "5.0.0", Revision: 1,
		SourceType: "manual", Author: "sample-corpus-independent-oracle", ReviewState: parserpolicy.ReviewStateDraft,
		CreatedAt: time.Unix(0, 0).UTC(),
		ResourceLimits: parserpolicy.ResourceLimits{
			MaxInputBytes: sampleMaxLineBytes, MaxFields: 64, MaxCaptures: 32,
			MaxRegexLength: 4096, MaxStageCount: 4, MaxAttempts: 1, ExecutionDeadlineMs: 1000,
		},
		Rules: []parserpolicy.ParserRule{{
			ID: "representative", Name: "Representative source contract", Priority: 1,
			ServiceSelectors: []string{service}, FailurePolicy: parserpolicy.FailurePolicyRejectRule,
			ProjectionMode: parserpolicy.ProjectionModeAll, CountPolicy: parserpolicy.CountPolicyExactSource,
			Stages: []parserpolicy.Stage{
				{ID: "header", Name: "Reviewed source grammar", Type: parserpolicy.StageTypeRegex, Config: map[string]interface{}{"pattern": pattern, "groups": groups}, FailurePolicy: parserpolicy.FailurePolicyRejectRule},
				{ID: "types", Name: "Reviewed numeric type", Type: parserpolicy.StageTypeTypeConversion, Config: map[string]interface{}{"conversions": map[string]string{"pid_status": "int"}}, FailurePolicy: parserpolicy.FailurePolicyRejectRule},
			},
		}},
	}
	if err := parserpolicy.SealParserPack(pack); err != nil {
		t.Fatal(err)
	}
	executor, err := parserpolicy.NewParserPackExecutor(pack)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(&parserpolicy.ExecutionRequest{Input: input, Service: service, SourceType: "file", Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Quarantined || result.EventPayload == nil {
		t.Fatalf("reviewed regex profile quarantined: %+v", result.ParseTelemetry)
	}
	return result.EventPayload.Fields
}

func loadSampleKeyContracts(t *testing.T) sampleKeyContracts {
	t.Helper()
	contracts := sampleKeyContracts{}
	decodeSampleJSON(t, sampleCorpusPath("keys.expected.json"), 96*1024, &contracts)
	return contracts
}

func sampleAllowedSourceKeys(contracts sampleKeyContracts) map[string]map[string]bool {
	result := map[string]map[string]bool{}
	groups := append(append([]sampleKeySet(nil), contracts.Families...), contracts.ReviewedExtensions...)
	for _, group := range groups {
		if result[group.Name] == nil {
			result[group.Name] = map[string]bool{}
		}
		for _, key := range group.Keys {
			result[group.Name][key] = true
		}
	}
	return result
}

func buildSampleReviewedProfile(t *testing.T, contracts sampleKeyContracts, family, slug string) *parserpolicy.ParserPack {
	t.Helper()
	var contractKeys, extensions []string
	for _, group := range contracts.Families {
		if group.Name == family {
			contractKeys = group.Keys
		}
	}
	for _, group := range contracts.ReviewedExtensions {
		if group.Name == family {
			extensions = group.Keys
		}
	}
	if len(contractKeys) == 0 || slug == "" {
		t.Fatalf("missing reviewed profile inputs for %s", family)
	}
	pack, err := BuildReviewedFamilyProfile(FamilyProfileSpec{
		ID: "sample_reviewed_" + slug, Family: family, ContractKeys: contractKeys,
		ReviewedExtensions: extensions, ReviewReference: "sample-logs/keys.expected.json schema 2",
	})
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

func sampleProfileOutputKey(pack *parserpolicy.ParserPack, source string) string {
	if alias := pack.Metadata["field_alias."+source]; alias != "" {
		return alias
	}
	return source
}

func sampleSimpleAssignmentValue(raw, key string) (string, bool) {
	marker := ":" + key + "="
	start := strings.Index(raw, marker)
	if start < 0 {
		return "", false
	}
	start += len(marker)
	end := len(raw)
	for _, separator := range []byte{':', '|'} {
		if offset := strings.IndexByte(raw[start:], separator); offset >= 0 && start+offset < end {
			end = start + offset
		}
	}
	return raw[start:end], true
}

func loadSampleManifest(t *testing.T) sampleManifest {
	t.Helper()
	manifest := sampleManifest{}
	decodeSampleJSON(t, sampleCorpusPath("manifest.json"), 128*1024, &manifest)
	return manifest
}

func loadSampleRecordContracts(t *testing.T) sampleRecordContracts {
	t.Helper()
	contracts := sampleRecordContracts{}
	decodeSampleJSON(t, sampleCorpusPath("records.expected.json"), 64*1024, &contracts)
	return contracts
}

func findSampleRecordContract(t *testing.T, family string) sampleRecordContract {
	t.Helper()
	for _, record := range loadSampleRecordContracts(t).Records {
		if record.Family == family {
			return record
		}
	}
	t.Fatalf("record contract for %s not found", family)
	return sampleRecordContract{}
}

func loadSampleArchiveMembers(t *testing.T) map[string][]byte {
	t.Helper()
	manifest := loadSampleManifest(t)
	archive := readSampleFile(t, safeSampleFixturePath(t, manifest.Source.Archive), sampleMaxArchiveBytes)
	members, err := readSampleArchive(archive, manifest.Bounds)
	if err != nil {
		t.Fatal(err)
	}
	return members
}

func readSampleArchive(data []byte, bounds sampleBounds) (map[string][]byte, error) {
	if int64(len(data)) == 0 || int64(len(data)) > bounds.MaxArchiveBytes {
		return nil, fmt.Errorf("sample archive exceeds archive limit")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("parse sample archive: %w", err)
	}
	if len(reader.File) > bounds.MaxEntries {
		return nil, fmt.Errorf("sample archive exceeds entry limit")
	}
	members := map[string][]byte{}
	var expanded int64
	for _, entry := range reader.File {
		if !safeSampleArchiveName(entry.Name) {
			return nil, fmt.Errorf("unsafe sample archive path %q", entry.Name)
		}
		if entry.Flags&1 != 0 {
			return nil, fmt.Errorf("encrypted sample archive member %q", entry.Name)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("sample archive symlink %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.UncompressedSize64 > uint64(bounds.MaxMemberBytes) {
			return nil, fmt.Errorf("sample archive member limit exceeded by %q", entry.Name)
		}
		expanded += int64(entry.UncompressedSize64)
		if expanded > bounds.MaxExpandedBytes {
			return nil, fmt.Errorf("sample archive expansion limit exceeded")
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		content, readErr := io.ReadAll(io.LimitReader(stream, bounds.MaxMemberBytes+1))
		closeErr := stream.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if int64(len(content)) > bounds.MaxMemberBytes || uint64(len(content)) != entry.UncompressedSize64 {
			return nil, fmt.Errorf("sample archive member limit or size mismatch for %q", entry.Name)
		}
		if _, duplicate := members[entry.Name]; duplicate {
			return nil, fmt.Errorf("duplicate sample archive member %q", entry.Name)
		}
		members[entry.Name] = content
	}
	return members, nil
}

func safeSampleArchiveName(name string) bool {
	if name == "" || strings.Contains(name, "\\") {
		return false
	}
	candidate := strings.TrimSuffix(name, "/")
	if candidate == "" || path.IsAbs(candidate) || strings.Contains(strings.Split(candidate, "/")[0], ":") {
		return false
	}
	cleaned := path.Clean(candidate)
	return cleaned == candidate && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func splitSampleCRLFLines(data []byte, maxLines, maxLineBytes int) ([][]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("sample text is empty")
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxLines {
		return nil, fmt.Errorf("sample text exceeds line limit")
	}
	for index, line := range lines {
		if len(line) > maxLineBytes {
			return nil, fmt.Errorf("sample line %d exceeds line-byte limit", index+1)
		}
		if !bytes.HasSuffix(line, []byte("\r\n")) {
			return nil, fmt.Errorf("sample line %d is not CRLF terminated", index+1)
		}
	}
	return lines, nil
}

func splitSampleFamilies(lines [][]byte) (sampleSplit, error) {
	headings := map[string]struct{ Name, Slug string }{}
	for _, family := range sampleFamilySpecs {
		headings[family.Heading] = struct{ Name, Slug string }{family.Name, family.Slug}
	}
	result := sampleSplit{Records: map[string][]byte{}, SourceLines: map[string][]int{}, LineFamily: map[int]string{}}
	currentSlug, currentName := "", ""
	seenHeadings := map[string]bool{}
	for index, line := range lines {
		number := index + 1
		body := strings.TrimSpace(string(bytes.TrimSuffix(line, []byte("\r\n"))))
		if body == "" {
			result.BlankLines = append(result.BlankLines, number)
			continue
		}
		result.NonblankLines++
		if heading, exists := headings[body]; exists {
			if seenHeadings[heading.Slug] {
				return result, fmt.Errorf("duplicate family heading %s", heading.Name)
			}
			seenHeadings[heading.Slug] = true
			currentSlug, currentName = heading.Slug, heading.Name
			result.HeadingLines = append(result.HeadingLines, number)
			continue
		}
		if currentSlug == "" {
			return result, fmt.Errorf("unlabeled nonblank source line %d", number)
		}
		result.Records[currentSlug] = append(result.Records[currentSlug], line...)
		result.SourceLines[currentSlug] = append(result.SourceLines[currentSlug], number)
		result.LineFamily[number] = currentName
		result.RecordLines++
	}
	if len(seenHeadings) != len(sampleFamilySpecs) {
		return result, fmt.Errorf("found %d family headings, want %d", len(seenHeadings), len(sampleFamilySpecs))
	}
	return result, nil
}

type sampleAssignedKeyScan struct {
	ByFamily map[string]map[string]bool
	ByLine   map[int]map[string]bool
}

func scanSampleAssignedKeys(data []byte) (sampleAssignedKeyScan, error) {
	lines, err := splitSampleCRLFLines(data, sampleMaxLines, sampleMaxLineBytes)
	if err != nil {
		return sampleAssignedKeyScan{}, err
	}
	split, err := splitSampleFamilies(lines)
	if err != nil {
		return sampleAssignedKeyScan{}, err
	}
	result := sampleAssignedKeyScan{ByFamily: map[string]map[string]bool{}, ByLine: map[int]map[string]bool{}}
	for lineNumber, family := range split.LineFamily {
		raw := string(bytes.TrimSuffix(lines[lineNumber-1], []byte("\r\n")))
		tail, webtop, err := sampleAssignmentTail(raw, family)
		if err != nil {
			return sampleAssignedKeyScan{}, fmt.Errorf("source line %d: %w", lineNumber, err)
		}
		keys := scanSampleTopLevelHeaders(tail, webtop)
		if result.ByFamily[family] == nil {
			result.ByFamily[family] = map[string]bool{}
		}
		result.ByLine[lineNumber] = keys
		for key := range keys {
			result.ByFamily[family][key] = true
		}
	}
	return result, nil
}

func sampleAssignmentTail(raw, family string) (string, bool, error) {
	switch family {
	case "WEBTOP/WEBMAIL":
		parts := strings.SplitN(raw, " ", 3)
		if len(parts) != 3 {
			return "", false, fmt.Errorf("WEBTOP record has no field tail")
		}
		return parts[2], true, nil
	case "PSS/PABD":
		index := strings.Index(raw, ";CARDDAV ")
		if index < 0 {
			return "", false, fmt.Errorf("PABD event marker missing")
		}
		return raw[index+len(";CARDDAV "):], false, nil
	case "MSS/CAPD":
		index := strings.Index(raw, ";CalDavResponse ")
		if index < 0 {
			return "", false, fmt.Errorf("CAPD event marker missing")
		}
		return raw[index+len(";CalDavResponse "):], false, nil
	default:
		parser, err := staticlayout.NewSMTPParser()
		if err != nil {
			return "", false, err
		}
		parsed, err := parser.Parse(raw)
		if err != nil {
			return "", false, err
		}
		value, _ := parsed.Fields["_kv_tail"].(string)
		return value, false, nil
	}
}

func scanSampleTopLevelHeaders(input string, whitespacePairs bool) map[string]bool {
	result := map[string]bool{}
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
			for index < len(input) && ((input[index] >= 'A' && input[index] <= 'Z') || (input[index] >= 'a' && input[index] <= 'z') || (input[index] >= '0' && input[index] <= '9') || input[index] == '_' || input[index] == '.' || input[index] == '-') {
				index++
			}
			if index > start && index < len(input) && (input[index] == '=' || input[index] == ';') && ((input[start] >= 'A' && input[start] <= 'Z') || (input[start] >= 'a' && input[start] <= 'z') || input[start] == '_') {
				result[input[start:index]] = true
			}
			if index > start {
				continue
			}
		}
		index++
	}
	return result
}

func parseSampleKeyInventory(data []byte) (map[string][]string, error) {
	lines, err := splitSampleCRLFLines(data, 512, 256)
	if err != nil {
		return nil, err
	}
	preamble := "it may have extra as well so in future we may add new key names, so plan accordingly for all Logs types to add custome key names for extraction with proper seperator or delimeter."
	if strings.TrimSpace(string(bytes.TrimSuffix(lines[0], []byte("\r\n")))) != preamble {
		return nil, fmt.Errorf("key inventory preamble changed")
	}
	headings := map[string]string{
		"IMAP all Known Key names for keyvalue pair extraction:":              "IMAP",
		"POP all Known Key names for keyvalue pair extraction:":               "POP",
		"Webtop or webmail all Known Key names for keyvalue pair extraction:": "WEBTOP/WEBMAIL",
		"MTA all Known Key names for keyvalue pair extraction:":               "MTA",
		"Pabd all Known Key names for keyvalue pair extraction:":              "PSS/PABD",
		"Capd all Known Key names for keyvalue pair extraction:":              "MSS/CAPD",
		"SMTP-IN all Known Key names for keyvalue pair extraction:":           "SMTP-IN",
		"SMTP-OUT all Known Key names for keyvalue pair extraction:":          "SMTP-OUT",
	}
	result := map[string][]string{}
	current := ""
	for index, line := range lines[1:] {
		text := strings.TrimSpace(string(bytes.TrimSuffix(line, []byte("\r\n"))))
		if text == "" {
			continue
		}
		if family, exists := headings[text]; exists {
			if _, duplicate := result[family]; duplicate {
				return nil, fmt.Errorf("duplicate key heading %s", family)
			}
			current = family
			result[current] = []string{}
			continue
		}
		if current == "" {
			return nil, fmt.Errorf("unlabeled key at source line %d", index+2)
		}
		result[current] = append(result[current], text)
	}
	return result, nil
}

func sampleCorpusPath(parts ...string) string {
	all := append([]string{"..", "..", "testdata", "migration", "sample-logs"}, parts...)
	return filepath.Join(all...)
}

func safeSampleFixturePath(t *testing.T, relative string) string {
	t.Helper()
	if !safeSampleArchiveName(relative) || strings.HasSuffix(relative, "/") {
		t.Fatalf("unsafe fixture path %q", relative)
	}
	return sampleCorpusPath(filepath.FromSlash(relative))
}

func readSampleFile(t *testing.T, filename string, limit int64) []byte {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(content)) > limit {
		t.Fatalf("fixture %s exceeds %d bytes", filename, limit)
	}
	return content
}

func decodeSampleJSON(t *testing.T, filename string, limit int64, output interface{}) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(readSampleFile(t, filename, limit)))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("fixture %s must contain exactly one JSON value", filename)
	}
}

func assertSampleJSONEqual(t *testing.T, actual, expected interface{}) {
	t.Helper()
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Fatalf("payload mismatch\nactual:   %s\nexpected: %s", actualJSON, expectedJSON)
	}
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func makeSampleZIP(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
