package migration

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	parserpolicy "nexora.local/nli-v20/parser-policy"
)

const testAlloy = `
loki.process "smtp" {
  stage.regex {
    expression = "^(?P<level>[A-Z]+) (?P<message>.*)$"
  }
  stage.replace {
    source = "message"
    expression = "secret"
    replace = "[redacted]"
  }
}
`

const testVector = `
[sources.smtp]
type = "file"
include = ["/var/log/smtp.log"]

[transforms.parse_smtp]
type = "remap"
inputs = ["smtp"]
source = '''
pattern = r'^(?P<level>[A-Z]+) (?P<message>.*)$'
parsed, err = parse_regex(.message, pattern)
.port = to_int(.port) ?? 0
.message = replace(.message, r'secret', "[redacted]")
del(.debug)
'''
`

func testUpload(name, format, content string) UploadedFile {
	data := []byte(content)
	digest := sha256.Sum256(data)
	return UploadedFile{Name: name, Format: format, Content: data, Size: int64(len(data)), Checksum: hex.EncodeToString(digest[:])}
}

func TestRealAlloyAndVectorImporters(t *testing.T) {
	batch, err := NewBatchProcessor().ProcessBatch([]UploadedFile{
		testUpload("smtp.alloy", "alloy", testAlloy),
		testUpload("smtp.toml", "vector", testVector),
	})
	if err != nil {
		t.Fatal(err)
	}
	if batch.TotalFiles != 2 || batch.ParsedFiles != 2 || batch.TranslatedFiles != 2 || batch.FailedFiles != 0 {
		t.Fatalf("unexpected accounting: %+v", batch)
	}
	for _, result := range batch.Results {
		if result.ParserPack == nil || result.ParserPack.ReviewState != parserpolicy.ReviewStateDraft {
			t.Fatalf("%s did not produce a draft pack", result.SourceFile)
		}
		if result.ParserPack.SourceHash != result.SourceSHA256 || result.ParserPack.SHA256 == "" {
			t.Fatalf("%s hashes not preserved", result.SourceFile)
		}
		if len(result.LineMappings) == 0 || len(result.Operations) == 0 {
			t.Fatalf("%s lacks structural provenance", result.SourceFile)
		}
		if result.ParityStatus != ParityUnverified {
			t.Fatalf("unsampled source parity = %q", result.ParityStatus)
		}
	}
}

func TestVectorVRLUnsupportedSemanticsAreExplicit(t *testing.T) {
	result, err := TranslateVector([]byte(testVector), "smtp.toml")
	if err != nil {
		t.Fatal(err)
	}
	seenRegex, seenConversion, seenReplace, seenDelete, seenCoalesce := false, false, false, false, false
	for _, operation := range result.Operations {
		switch operation.Kind {
		case "vrl.parse_regex":
			seenRegex = operation.Status == "translated"
		case "vrl.to_int":
			seenConversion = operation.Status == "translated"
		case "vrl.replace":
			seenReplace = operation.Status == "translated"
		case "vrl.del":
			seenDelete = operation.Status == "translated"
		case "vrl.null_coalescing":
			seenCoalesce = operation.Status == "unsupported"
		}
	}
	if !seenRegex || !seenConversion || !seenReplace || !seenDelete || !seenCoalesce {
		t.Fatalf("VRL inventory incomplete: regex=%v conversion=%v replace=%v delete=%v coalesce=%v", seenRegex, seenConversion, seenReplace, seenDelete, seenCoalesce)
	}
}

func TestMigrationOutputIsDeterministic(t *testing.T) {
	first, err := TranslateVector([]byte(testVector), "deterministic.toml")
	if err != nil {
		t.Fatal(err)
	}
	second, err := TranslateVector([]byte(testVector), "deterministic.toml")
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(second)
	if !bytes.Equal(left, right) {
		t.Fatalf("same source generated different reports")
	}
}

func TestBatchBoundsAndChecksums(t *testing.T) {
	processor := &BatchProcessor{MaxFileBytes: 32, MaxFiles: 1, MaxTotalBytes: 48}
	if _, err := processor.ProcessBatch(nil); err == nil {
		t.Fatal("empty batch accepted")
	}
	if _, err := processor.ProcessBatch([]UploadedFile{testUpload("a.alloy", "alloy", testAlloy), testUpload("b.alloy", "alloy", testAlloy)}); err == nil {
		t.Fatal("file-count limit not enforced")
	}
	bad := testUpload("bad.alloy", "alloy", "loki.process \"x\" {}")
	bad.Checksum = strings.Repeat("0", 64)
	batch, err := NewBatchProcessor().ProcessBatch([]UploadedFile{bad})
	if err != nil {
		t.Fatal(err)
	}
	if batch.FailedFiles != 1 || !strings.Contains(batch.Results[0].Errors[0], "checksum") {
		t.Fatalf("checksum mismatch not reported: %+v", batch.Results[0])
	}
}

func TestArchiveIsBoundedAndNeverExtracted(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, _ := writer.Create("nested/smtp.txt")
	_, _ = entry.Write([]byte(testAlloy))
	_ = writer.Close()
	batch, err := NewBatchProcessor().ProcessArchive(buffer.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if batch.TotalFiles != 1 || batch.Results[0].SourceFile != "nested/smtp.txt" {
		t.Fatalf("archive result: %+v", batch)
	}

	buffer.Reset()
	writer = zip.NewWriter(&buffer)
	entry, _ = writer.Create("../escape.alloy")
	_, _ = entry.Write([]byte(testAlloy))
	_ = writer.Close()
	if _, err := NewBatchProcessor().ProcessArchive(buffer.Bytes()); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("archive traversal accepted: %v", err)
	}
}
