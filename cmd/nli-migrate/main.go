package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"nexora.local/nli-v20/internal/migration"
	parserpolicy "nexora.local/nli-v20/parser-policy"
)

type regressionFile struct {
	SourceFile string                        `json:"source_file"`
	Cases      []parserpolicy.RegressionCase `json:"cases"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "nli-migrate:", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("nli-migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "Alloy/River, Vector TOML/VRL, or ZIP input")
	format := flags.String("format", "", "optional format override: alloy, river, vector, toml")
	regressionPath := flags.String("regressions", "", "optional exact raw-to-expected regression JSON")
	output := flags.String("output", "-", "report path, or - for stdout")
	maxFileMiB := flags.Int64("max-file-mib", 10, "maximum uncompressed source size")
	maxFiles := flags.Int("max-files", 100, "maximum files per archive")
	maxTotalMiB := flags.Int64("max-total-mib", 50, "maximum archive/expanded batch size")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if strings.TrimSpace(*input) == "" {
		return fmt.Errorf("-input is required")
	}
	if *maxFileMiB < 1 || *maxFileMiB > 16 || *maxFiles < 1 || *maxFiles > 1000 || *maxTotalMiB < 1 || *maxTotalMiB > 256 {
		return fmt.Errorf("invalid resource limits")
	}
	processor := &migration.BatchProcessor{MaxFileBytes: *maxFileMiB << 20, MaxFiles: *maxFiles, MaxTotalBytes: *maxTotalMiB << 20}
	data, err := readBounded(*input, (*maxTotalMiB<<20)+1)
	if err != nil {
		return err
	}
	var report *migration.BatchResult
	if strings.EqualFold(filepath.Ext(*input), ".zip") {
		if *regressionPath != "" {
			return fmt.Errorf("-regressions is supported only for a single source file")
		}
		report, err = processor.ProcessArchive(data)
	} else {
		regressions, loadErr := loadRegressions(*regressionPath, filepath.Base(*input))
		if loadErr != nil {
			return loadErr
		}
		report, err = processor.ProcessBatch([]migration.UploadedFile{{
			Name: filepath.Base(*input), Content: data, Size: int64(len(data)), Format: *format, Regressions: regressions,
		}})
	}
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if *output == "-" {
		_, err = stdout.Write(encoded)
		return err
	}
	return atomicWrite(*output, encoded)
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) >= limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit-1)
	}
	return data, nil
}

func loadRegressions(path, sourceName string) ([]parserpolicy.RegressionCase, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readBounded(path, 4<<20)
	if err != nil {
		return nil, err
	}
	var fixture regressionFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		return nil, fmt.Errorf("decode regressions: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("regression file must contain one JSON object")
	}
	if fixture.SourceFile != "" && fixture.SourceFile != sourceName {
		return nil, fmt.Errorf("regression source_file %q does not match %q", fixture.SourceFile, sourceName)
	}
	return fixture.Cases, nil
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil && filepath.Dir(path) != "." {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o640); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
