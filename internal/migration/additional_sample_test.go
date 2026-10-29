package migration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/adapter"
	"nexora.local/nli-v20/internal/engine"
)

func TestStandaloneUserSampleLinesExecuteV20Runtime(t *testing.T) {
	path := "../../testdata/migration/sample-logs/source/1st_test_sample_logs.txt"
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	parser, err := adapter.NewParserPackAdapter()
	if err != nil {
		t.Fatal(err)
	}
	deps := engine.Default()
	deps.SetV5Parser(parser)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	lines, nonempty, executed := 0, 0, 0
	for scanner.Scan() {
		lines++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		nonempty++
		result := engine.Parse(raw, engine.Source{Service: "auto", File: "1st_test_sample_logs.txt"}, deps)
		if result.Meta.Route == "" {
			t.Fatalf("line %d produced no route", lines)
		}
		executed++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != 50 || nonempty != 50 || executed != 50 {
		t.Fatalf("supplied standalone corpus accounting: lines=%d nonempty=%d executed=%d; want 50 each", lines, nonempty, executed)
	}
}

func TestV20CatalogCopiesAndSampleInventoryAreConsistent(t *testing.T) {
	canonical, err := os.ReadFile("../../configs/parser-catalog/nli-v20-parser-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile("../../configs/catalog/nli-v20-parser-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, embedded) {
		t.Fatal("embedded and canonical parser catalog copies drifted")
	}
	manifestBytes, err := os.ReadFile("../../testdata/migration/sample-logs/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SampleLogRecordCount int `json:"sample_log_record_count"`
		Additional           []struct {
			Path     string `json:"path"`
			SHA256   string `json:"sha256"`
			Nonblank int    `json:"nonblank_record_lines"`
		} `json:"additional_source_files"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SampleLogRecordCount != 728 || len(manifest.Additional) != 3 {
		t.Fatalf("sample inventory incomplete: total=%d files=%d", manifest.SampleLogRecordCount, len(manifest.Additional))
	}
	if manifest.Additional[0].Path != "source/1st_test_sample_logs.txt" || manifest.Additional[0].Nonblank != 50 || len(manifest.Additional[0].SHA256) != 64 {
		t.Fatalf("standalone sample provenance missing: %+v", manifest.Additional[0])
	}
}
