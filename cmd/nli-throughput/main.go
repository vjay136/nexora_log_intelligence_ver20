// Command nli-throughput qualifies the production agent against generated JSONL.
// It includes actual file generation, tail/read/parse, durable local output and
// checkpointing, then validates the output with bounded memory.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/agent"
	"nexora.local/nli-v20/internal/output"
)

const maxGeneratedLineBytes = 1 << 20
const maxOutputRecordBytes = 2 << 20
const fiveGiB = int64(5) << 30

type report struct {
	GeneratedAtUTC       time.Time `json:"generated_at_utc"`
	HostOS               string    `json:"host_os"`
	HostArch             string    `json:"host_arch"`
	LogicalCPUs          int       `json:"logical_cpus"`
	GoMaxProcs           int       `json:"go_max_procs"`
	RequestedInputGiB    float64   `json:"requested_input_gib"`
	InputFiles           int       `json:"input_files"`
	SourceWorkers        int       `json:"source_workers"`
	InputBytes           int64     `json:"input_bytes"`
	InputRecords         int64     `json:"input_records"`
	OutputBytes          int64     `json:"output_bytes"`
	OutputRecords        int64     `json:"output_records"`
	InvalidOutputRecords int64     `json:"invalid_output_records"`
	OversizedOutputLines int64     `json:"oversized_output_lines"`
	QuarantineRecords    int64     `json:"quarantine_records"`
	InvalidQuarantine    int64     `json:"invalid_quarantine_records"`
	OversizedQuarantine  int64     `json:"oversized_quarantine_records"`
	CheckpointedFiles    int       `json:"checkpointed_files"`
	CheckpointedBytes    int64     `json:"checkpointed_input_bytes"`
	CheckpointedRecords  int64     `json:"checkpointed_input_records"`
	GenerationSeconds    float64   `json:"generation_seconds"`
	ProcessingSeconds    float64   `json:"processing_seconds"`
	ValidationSeconds    float64   `json:"validation_seconds"`
	InputMiBPerSecond    float64   `json:"input_mib_per_second"`
	RecordsPerSecond     float64   `json:"records_per_second"`
	TargetSeconds        float64   `json:"target_seconds"`
	AccountingPassed     bool      `json:"accounting_passed"`
	CheckpointPassed     bool      `json:"checkpoint_passed"`
	CheckpointError      string    `json:"checkpoint_error,omitempty"`
	TargetMetOnThisHost  bool      `json:"target_met_on_this_host"`
	ParserPackSource     string    `json:"parser_pack_source_directory"`
	ParserPackDirectory  string    `json:"parser_pack_directory_used"`
	DurableFileOutput    bool      `json:"durable_file_output"`
	Scope                string    `json:"scope"`
}

type generatedInput struct {
	path    string
	bytes   int64
	records int64
}

type savedCursor struct {
	Offset int64 `json:"offset"`
	Line   int64 `json:"line"`
}

func main() {
	var gib float64
	var lineBytes, sources, workers int
	var workDir, reportPath, parserDir, cpuProfilePath string
	var keep bool
	flag.Float64Var(&gib, "gib", 5, "minimum aggregate generated input size in GiB")
	flag.IntVar(&lineBytes, "line-bytes", 4096, "approximate JSON input record size")
	flag.IntVar(&sources, "sources", 4, "number of independent generated input files (1..64)")
	flag.IntVar(&workers, "source-workers", 0, "production agent source workers; 0 selects its CPU-based default")
	flag.StringVar(&workDir, "work-dir", "", "work directory; defaults to a temporary directory")
	flag.StringVar(&reportPath, "report", "THROUGHPUT_5G.json", "JSON report path")
	flag.StringVar(&parserDir, "parser-pack-dir", "configs/parsers", "reviewed ParserPack directory")
	flag.StringVar(&cpuProfilePath, "cpu-profile", "", "optional CPU profile path for qualification diagnostics")
	flag.BoolVar(&keep, "keep", false, "retain temporary work directory and generated files")
	flag.Parse()

	if gib <= 0 || gib > 100 || lineBytes < 256 || lineBytes > maxGeneratedLineBytes || sources < 1 || sources > 64 || workers < 0 || workers > 64 {
		fatalf("gib must be >0 and <=100, line-bytes 256..1048576, sources 1..64, and source-workers 0..64")
	}
	absParserDir, err := filepath.Abs(parserDir)
	if err != nil {
		fatalf("resolve parser directory: %v", err)
	}
	if info, err := os.Stat(absParserDir); err != nil || !info.IsDir() {
		fatalf("parser pack directory is unavailable: %s", absParserDir)
	}
	ownedTemp := false
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "nli-v20-throughput-")
		if err != nil {
			fatalf("create work directory: %v", err)
		}
		ownedTemp = true
	} else if err = os.MkdirAll(workDir, 0700); err != nil {
		fatalf("create work directory: %v", err)
	}
	if ownedTemp && !keep {
		defer os.RemoveAll(workDir)
	}

	targetBytes := int64(gib * float64(uint64(1)<<30))
	inputPaths := make([]generatedInput, 0, sources)
	inputTotal, recordTotal := int64(0), int64(0)
	generationStart := time.Now()
	remaining := targetBytes
	for i := 0; i < sources; i++ {
		filesLeft := int64(sources - i)
		share := (remaining + filesLeft - 1) / filesLeft
		path := filepath.Join(workDir, fmt.Sprintf("input-%03d.jsonl", i+1))
		written, records, genErr := generateInput(path, share, lineBytes)
		if genErr != nil {
			fatalf("generate input %s: %v", path, genErr)
		}
		inputPaths = append(inputPaths, generatedInput{path: path, bytes: written, records: records})
		inputTotal += written
		recordTotal += records
		remaining -= written
		if remaining < 0 {
			remaining = 0
		}
	}
	generationDuration := time.Since(generationStart)

	privateParserDir := filepath.Join(workDir, "parser-packs")
	if err := copyParserPacks(absParserDir, privateParserDir); err != nil {
		fatalf("prepare private parser pack snapshot: %v", err)
	}
	outputPath := filepath.Join(workDir, "normalised.jsonl")
	stateDir := filepath.Join(workDir, "state")
	inputs := make([]agent.InputConfig, 0, len(inputPaths))
	for _, input := range inputPaths {
		inputs = append(inputs, agent.InputConfig{Path: input.path, Service: "auto"})
	}
	if workers == 0 {
		workers = runtime.NumCPU()
		if workers > 8 {
			workers = 8
		}
		if workers < 1 {
			workers = 1
		}
	}
	if workers > sources {
		workers = sources
	}
	config := agent.Config{
		Version: 2, Inputs: inputs, StateDir: stateDir, ParserPackDir: privateParserDir,
		Outputs:          []output.Config{{Type: "file", Path: outputPath, Routes: []string{"normal", "warning"}}},
		TelemetryOutputs: []output.Config{{Type: "discard", Routes: []string{"telemetry"}}},
		PollMS:           50, SourceWorkers: workers,
	}

	processingStart := time.Now()
	if cpuProfilePath != "" {
		profile, profileErr := os.Create(cpuProfilePath)
		if profileErr != nil {
			fatalf("create CPU profile: %v", profileErr)
		}
		if profileErr = pprof.StartCPUProfile(profile); profileErr != nil {
			profile.Close()
			fatalf("start CPU profile: %v", profileErr)
		}
		defer profile.Close()
	}
	if err := agent.Run(context.Background(), config, true); err != nil {
		fatalf("agent qualification failed: %v", err)
	}
	if cpuProfilePath != "" {
		pprof.StopCPUProfile()
	}
	processingDuration := time.Since(processingStart)

	validationStart := time.Now()
	outputBytes, outputRecords, invalid, oversized, err := validateJSONL(outputPath, maxOutputRecordBytes)
	if err != nil {
		fatalf("validate output: %v", err)
	}
	quarantinePath := filepath.Join(stateDir, "quarantine", "records.jsonl")
	_, quarantineRecords, quarantineInvalid, quarantineOversized, err := validateJSONL(quarantinePath, maxOutputRecordBytes)
	if err != nil {
		fatalf("validate quarantine output: %v", err)
	}
	checkpointFiles, checkpointBytes, checkpointRecords, checkpointErr := validateCheckpoints(filepath.Join(stateDir, "offsets.json"), inputPaths)
	validationDuration := time.Since(validationStart)
	seconds := processingDuration.Seconds()
	result := report{
		GeneratedAtUTC: time.Now().UTC(), HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
		LogicalCPUs: runtime.NumCPU(), GoMaxProcs: runtime.GOMAXPROCS(0), RequestedInputGiB: gib,
		InputFiles: len(inputPaths), SourceWorkers: workers,
		InputBytes: inputTotal, InputRecords: recordTotal, OutputBytes: outputBytes, OutputRecords: outputRecords,
		InvalidOutputRecords: invalid, OversizedOutputLines: oversized,
		QuarantineRecords: quarantineRecords, InvalidQuarantine: quarantineInvalid,
		OversizedQuarantine: quarantineOversized, CheckpointedFiles: checkpointFiles,
		CheckpointedBytes: checkpointBytes, CheckpointedRecords: checkpointRecords,
		GenerationSeconds: generationDuration.Seconds(), ProcessingSeconds: seconds,
		ValidationSeconds: validationDuration.Seconds(), TargetSeconds: 600,
		ParserPackSource: absParserDir, ParserPackDirectory: privateParserDir, DurableFileOutput: true,
		Scope: "generated JSONL across independent source files through the production agent read, ParserPack attempt/fallback parse, durable local JSONL output and durable checkpoints; includes output/quarantine JSON validation; metrics are host-specific",
	}
	if seconds > 0 {
		result.InputMiBPerSecond = float64(inputTotal) / (1024 * 1024) / seconds
		result.RecordsPerSecond = float64(recordTotal) / seconds
	}
	result.AccountingPassed = inputTotal >= targetBytes && recordTotal == outputRecords+quarantineRecords && invalid == 0 && oversized == 0 && quarantineInvalid == 0 && quarantineOversized == 0 && quarantineRecords == 0
	result.CheckpointPassed = checkpointErr == nil && checkpointFiles == len(inputPaths) && checkpointBytes == inputTotal && checkpointRecords == recordTotal
	if checkpointErr != nil {
		result.CheckpointError = checkpointErr.Error()
	}
	result.TargetMetOnThisHost = result.AccountingPassed && result.CheckpointPassed && inputTotal >= fiveGiB && seconds <= result.TargetSeconds

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fatalf("encode report: %v", err)
	}
	if err := os.WriteFile(reportPath, append(data, '\n'), 0644); err != nil {
		fatalf("write report: %v", err)
	}
	fmt.Printf("input=%d bytes records=%d output=%d records=%d quarantine=%d workers=%d processing=%s throughput=%.2f MiB/s accounting=%v checkpoint=%v target_met=%v report=%s\n",
		inputTotal, recordTotal, outputBytes, outputRecords, quarantineRecords, workers,
		processingDuration.Round(time.Millisecond), result.InputMiBPerSecond,
		result.AccountingPassed, result.CheckpointPassed, result.TargetMetOnThisHost, reportPath)
	if !result.AccountingPassed || !result.CheckpointPassed {
		os.Exit(1)
	}
}

func generateInput(path string, target int64, approximateLineBytes int) (int64, int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, 0, err
	}
	writer := bufio.NewWriterSize(file, 4<<20)
	fixed := `{"timestamp":"2026-10-14T00:00:00Z","service":"bench","hostname":"nli-throughput","level":"INFO","message":"`
	suffix := `"}` + "\n"
	padding := approximateLineBytes - len(fixed) - len(suffix)
	if padding < 1 {
		padding = 1
	}
	line := []byte(fixed + strings.Repeat("x", padding) + suffix)
	var bytesWritten, records int64
	for bytesWritten < target {
		n, writeErr := writer.Write(line)
		bytesWritten += int64(n)
		if n > 0 {
			records++
		}
		if writeErr != nil {
			err = writeErr
			break
		}
	}
	if flushErr := writer.Flush(); err == nil {
		err = flushErr
	}
	if syncErr := file.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return bytesWritten, records, err
}

func copyParserPacks(source, destination string) error {
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("parser input %q is not a regular file", entry.Name())
		}
		input, err := os.Open(filepath.Join(source, entry.Name()))
		if err != nil {
			return err
		}
		output, err := os.OpenFile(filepath.Join(destination, entry.Name()), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeInputErr != nil {
			return closeInputErr
		}
		if closeOutputErr != nil {
			return closeOutputErr
		}
	}
	return nil
}

// validateJSONL caps memory even if an output line violates the expected bound.
func validateJSONL(path string, maxLineBytes int) (bytesRead, records, invalid, oversized int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	line := make([]byte, 0, min(maxLineBytes, 64*1024))
	tooLarge := false
	for {
		chunk, readErr := reader.ReadSlice('\n')
		bytesRead += int64(len(chunk))
		if !tooLarge {
			if len(line)+len(chunk) > maxLineBytes+1 {
				tooLarge = true
				line = nil
				oversized++
			} else {
				line = append(line, chunk...)
			}
		}
		lineEnded := len(chunk) > 0 && chunk[len(chunk)-1] == '\n'
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if lineEnded || (errors.Is(readErr, io.EOF) && (len(line) > 0 || tooLarge)) {
			records++
			if tooLarge || !json.Valid(bytes.TrimSpace(line)) {
				invalid++
			}
			line = line[:0]
			tooLarge = false
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return bytesRead, records, invalid, oversized, nil
			}
			return bytesRead, records, invalid, oversized, readErr
		}
	}
}

func validateCheckpoints(path string, inputs []generatedInput) (files int, bytes, records int64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, err
	}
	var saved map[string]savedCursor
	if err := json.Unmarshal(data, &saved); err != nil {
		return 0, 0, 0, err
	}
	for _, input := range inputs {
		cursor, ok := saved[input.path]
		if !ok {
			return files, bytes, records, fmt.Errorf("checkpoint missing input %s", input.path)
		}
		if cursor.Offset != input.bytes || cursor.Line != input.records {
			return files, bytes, records, fmt.Errorf("checkpoint for %s is offset=%d line=%d, expected offset=%d line=%d", input.path, cursor.Offset, cursor.Line, input.bytes, input.records)
		}
		files++
		bytes += cursor.Offset
		records += cursor.Line
	}
	return files, bytes, records, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "nli-throughput: "+format+"\n", args...)
	os.Exit(1)
}
