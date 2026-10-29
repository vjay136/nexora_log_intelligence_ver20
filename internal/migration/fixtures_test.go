package migration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	parserpolicy "nexora.local/nli-v20/parser-policy"
)

type corpusFixture struct {
	Family string
	Path   string
	SHA256 string
}

var suppliedCorpus = []corpusFixture{
	{"ORCHESTRATION", "orchestration/main-combined.alloy", "65758781d889dd91c6ae2b330241411e55e2f2f0fe966c43f16871ca75d91f44"},
	{"SMTP-IN", "smtp-in/main.alloy", "ee40347d23038514551263b00e7763446d720d92ad550e4a32f5d729c8a6ccd8"},
	{"IMAP", "imap/imap_parse.toml", "ebee96a564e0dd214fccc10c84826519c508791bd05a89d286e8901062fa4ceb"},
	{"WEBTOP-WEBMAIL", "webtop-webmail/webtop_processor.alloy", "75f921c558463bf110fdb249322f2d4e59c8c398849b95280ff928bce441b7d8"},
	{"WEBTOP-WEBMAIL", "webtop-webmail/main.alloy", "cab586ec877a2f4c5037e86a998025349b87341f0f81573a3370632e3bfad4fb"},
	{"IMAP", "imap/imapserv_processor.alloy", "1ac70be4cdb72b2a8c1c8f8d965cfa280945facc3805eae9f05b57ac6d12f99d"},
	{"SMTP-IN", "smtp-in/smtp-in_processor.alloy", "c824e6d8bdcff359ec2714ebf3d7d3875aa9270442e20dbf896e1b3ca99cd8c6"},
	{"POP", "pop/pop_parse.toml", "27ddbbb14601bca17a561c760155045929033fb2331f9cf0674296cf7c08f533"},
	{"MTA", "mta/mta_processor.alloy", "f2940d4c893c6bf28c5ec058d82f0118546b258b94dd3a361121dc9fe1a3d8a0"},
	{"POP", "pop/popserv_processor.alloy", "830aeb1e269ab6720e55af2d35c045085090188423f59208966e12a06f5870c8"},
	{"SMTP-IN", "smtp-in/smtp-in_universal.alloy", "9e8f5e07b75125b02df983bbb4c88973b38297aff71ba4a7690db0d8d90e3e27"},
	{"PABD-PSS", "pabd-pss/pabd_processor.alloy", "08f9c1ca5728a2a79f2d1aba6240684244e8b39e3062b1cd73c6d55e4c1b3bd6"},
	{"CAPD-MSS", "capd-mss/capd_processor.alloy", "9f09660d64a9469c1aa102087d5d0e3133d23997e14ce665828f3a6fa8d7ac87"},
	{"ORCHESTRATION", "orchestration/main-duplicate.alloy", "65758781d889dd91c6ae2b330241411e55e2f2f0fe966c43f16871ca75d91f44"},
	{"SMTP-OUT", "smtp-out/smtp-out_processor.alloy", "24ab85c72bb752a0d39c5be9d0096ffd0b8dd1ec2c6e30fa61af5dd2b8adc04f"},
	{"SMTP-OUT", "smtp-out/main.alloy", "e002a47d5c437a61c02c7bdfc06687bb07f27d59fee5af999524725a4beeed21"},
}

type regressionFile struct {
	SourceFile string                        `json:"source_file"`
	Cases      []parserpolicy.RegressionCase `json:"cases"`
}

func TestAllSixteenUploadedSourcesAreByteAccounted(t *testing.T) {
	if len(suppliedCorpus) != 16 {
		t.Fatalf("fixture count = %d", len(suppliedCorpus))
	}
	seen := map[string]bool{}
	for _, fixture := range suppliedCorpus {
		if seen[fixture.Path] {
			t.Fatalf("duplicate fixture path %s", fixture.Path)
		}
		seen[fixture.Path] = true
		content, err := os.ReadFile(corpusPath(fixture.Path))
		if err != nil {
			t.Fatalf("read %s: %v", fixture.Path, err)
		}
		digest := sha256.Sum256(content)
		if actual := hex.EncodeToString(digest[:]); actual != fixture.SHA256 {
			t.Errorf("%s SHA-256 = %s, want %s", fixture.Path, actual, fixture.SHA256)
		}
	}
}

func TestSuppliedFamilyCorpusImportAndParityAccounting(t *testing.T) {
	regressions := loadRegressionFile(t)
	files := make([]UploadedFile, 0, len(suppliedCorpus))
	for _, fixture := range suppliedCorpus {
		content, err := os.ReadFile(corpusPath(fixture.Path))
		if err != nil {
			t.Fatal(err)
		}
		file := testUpload(filepath.Base(fixture.Path), detectFormat(fixture.Path, content), string(content))
		file.Content = content
		file.Size = int64(len(content))
		file.Checksum = fixture.SHA256
		if filepath.Base(fixture.Path) == regressions.SourceFile {
			file.Regressions = regressions.Cases
		}
		files = append(files, file)
	}
	batch, err := NewBatchProcessor().ProcessBatch(files)
	if err != nil {
		t.Fatal(err)
	}
	if batch.TotalFiles != 16 || batch.ParsedFiles != 16 || batch.TranslatedFiles != 11 || batch.VerifiedFiles != 0 || batch.FailedFiles != 0 {
		t.Fatalf("corpus accounting: total=%d parsed=%d translated=%d verified=%d failed=%d", batch.TotalFiles, batch.ParsedFiles, batch.TranslatedFiles, batch.VerifiedFiles, batch.FailedFiles)
	}
	for index, result := range batch.Results {
		fixture := suppliedCorpus[index]
		if result.SourceSHA256 != fixture.SHA256 {
			t.Errorf("%s result hash mismatch", fixture.Path)
		}
		if !result.Success {
			t.Errorf("unexpected failure for %s: %v", fixture.Path, result.Errors)
			continue
		}
		for _, assessment := range result.RuleAssessments {
			if assessment.SampleCount == 0 && assessment.ParityStatus != ParityUnverified {
				t.Errorf("%s rule %s without samples marked %s", fixture.Path, assessment.RuleID, assessment.ParityStatus)
			}
		}
	}
	families := []string{"IMAP", "POP", "MTA", "SMTP-IN", "SMTP-OUT", "WEBTOP-WEBMAIL", "PABD-PSS", "CAPD-MSS"}
	for _, family := range families {
		if batch.Summary.ParserFamilyAnalysis[family].Sources == 0 {
			t.Errorf("family %s missing from report", family)
		}
	}
}

func TestExactRegressionFixtureDoesNotClaimUnsupportedParity(t *testing.T) {
	regressions := loadRegressionFile(t)
	content, err := os.ReadFile(corpusPath("smtp-in/smtp-in_universal.alloy"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := TranslateAlloy(content, "smtp-in_universal.alloy", regressions.Cases...)
	if err != nil {
		t.Fatal(err)
	}
	if result.ParityStatus != ParityFailed || len(result.RegressionOutcomes) != 1 || result.RegressionOutcomes[0].Passed || result.RegressionOutcomes[0].Error != "generated parser quarantined the sample" {
		encoded, _ := json.MarshalIndent(result.RegressionOutcomes, "", "  ")
		t.Fatalf("unsupported source semantics claimed parity: %s", encoded)
	}
	if result.ParserPack == nil || len(result.UnsupportedConstructs) == 0 {
		t.Fatal("source-derived executable stages or unsupported constructs were omitted")
	}
	for _, assessment := range result.RuleAssessments {
		if assessment.ParityStatus == ParityPassed {
			t.Fatalf("rule %s was marked passed without executable end-to-end parity", assessment.RuleID)
		}
	}
	for _, operation := range result.Operations {
		if operation.Status == "translated" && operation.IRStageID == "" {
			t.Fatalf("non-stage operation was reported as translated: %+v", operation)
		}
	}
}

func TestCompatibilityReportOrderingIsDeterministic(t *testing.T) {
	content := []byte(testVector)
	files := []UploadedFile{testUpload("imap.toml", "vector", string(content)), testUpload("pop.toml", "vector", string(content))}
	first, _ := NewBatchProcessor().ProcessBatch(files)
	second, _ := NewBatchProcessor().ProcessBatch(files)
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(second)
	if !bytes.Equal(left, right) {
		t.Fatal("compatibility report is not deterministic")
	}
	keys := make([]string, 0, len(first.Summary.RequiredRepairs))
	keys = append(keys, first.Summary.RequiredRepairs...)
	if !sort.StringsAreSorted(keys) {
		t.Fatal("repair list is not sorted")
	}
}

func loadRegressionFile(t *testing.T) regressionFile {
	t.Helper()
	data, err := os.ReadFile(corpusPath("smtp-in/smtp-golden.regressions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture regressionFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SourceFile == "" || len(fixture.Cases) == 0 {
		t.Fatal("regression fixture is empty")
	}
	return fixture
}

func corpusPath(relative string) string {
	return filepath.Join("..", "..", "testdata", "migration", filepath.FromSlash(relative))
}
