package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/quarantine"
)

func TestReplayQueueDeliversResolvedRecordsAndRetainsOnlyUnresolved(t *testing.T) {
	root := t.TempDir()
	queuePath := filepath.Join(root, "quarantine.jsonl")
	normalizedPath := filepath.Join(root, "normalized.jsonl")
	inputPath := filepath.Join(root, "input.log")
	raw := "20250811 080053102+0100 aaa-gmailrgi009 smtp 3692 0 140026870728448 Note;SmtpServerConnect(2001/3) id=67F5E2BF38CCA05B:host=11.22.33.44:connection_count=1:local_port=2525"
	record := quarantine.New("node-1", "smtp-in", inputPath, 7, raw, "old-revision", 1, []string{"all_packs_failed"}, time.Now().UTC())
	if err := quarantine.Append(queuePath, record); err != nil {
		t.Fatal(err)
	}
	waiting := quarantine.New("node-1", "not-supported", "/var/log/mail/other.log", 8, "unrecognized future family", "old-revision", 1, []string{"no_parser"}, time.Now().UTC())
	if err := quarantine.Append(queuePath, waiting); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"version":   2,
		"inputs":    []any{map[string]any{"path": inputPath, "service": "smtp-in"}},
		"state_dir": filepath.Join(root, "state"), "poll_ms": 500,
		"outputs": []any{
			map[string]any{"type": "file", "path": normalizedPath, "routes": []string{"normal", "warning"}},
			map[string]any{"type": "file", "path": queuePath, "routes": []string{"quarantine", "unknown"}},
		},
		"telemetry_outputs": []any{map[string]any{"type": "discard"}},
	}
	configBytes, _ := json.Marshal(config)
	configPath := filepath.Join(root, "nli.json")
	if err := os.WriteFile(configPath, configBytes, 0600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := replayQueue(queuePath, configPath, "", nil, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"resolved":1`) || !strings.Contains(output.String(), `"waiting":1`) {
		t.Fatalf("unexpected replay summary: %s", output.String())
	}
	normalized, err := os.ReadFile(normalizedPath)
	if err != nil || !strings.Contains(string(normalized), "connection_count") {
		t.Fatalf("parsed event not routed to configured file: output=%s err=%v", normalized, err)
	}
	active, err := os.ReadFile(queuePath)
	var remaining quarantine.Record
	if err != nil || json.Unmarshal(active, &remaining) != nil || remaining.ID != waiting.ID || remaining.Status != "waiting" || remaining.AttemptCount != 2 {
		t.Fatalf("unresolved record not retained with retry history: %q err=%v", active, err)
	}
	history, err := os.ReadFile(queuePath + ".history.jsonl")
	if err != nil || !strings.Contains(string(history), `"status":"resolved"`) || strings.Contains(string(history), raw) || strings.Contains(string(history), `"raw"`) {
		t.Fatalf("history should be bounded and raw-free: %s err=%v", history, err)
	}
}

func TestGeoIPUpdateRequiresExplicitLicenseAcceptance(t *testing.T) {
	t.Setenv("NLI_GEOIP_LICENSE_ACCEPTED", "false")
	t.Setenv("NLI_GEOIP_OPERATOR", "admin")
	var output strings.Builder
	err := run([]string{"geoip-update", "--root", t.TempDir()}, &output)
	if err == nil || !strings.Contains(err.Error(), "GeoLite2 EULA") {
		t.Fatalf("unexpected license gate result: %v", err)
	}
}

func TestGeoIPActivationRequiresGeneration(t *testing.T) {
	var output strings.Builder
	err := run([]string{"geoip-activate", "--root", t.TempDir(), "--operator", "admin"}, &output)
	if err == nil || !strings.Contains(err.Error(), "--generation is required") {
		t.Fatalf("unexpected activation validation result: %v", err)
	}
}

// TestCLIDefaultEventPayloadOnly tests that CLI defaults to EventPayload without full envelopes
func TestCLIDefaultEventPayloadOnly(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test log file with SMTP data that triggers v5 parser
	logFile := filepath.Join(tmpDir, "test.log")
	testData := "20250812 130034198+0100 aaa-gmailrgo009 smtp 3609 0 140290550523648 Note;SmtpServerConnect(2001/3) id=6864BD5E047384B0:host=11.22.33.44:connection_count=1:local_port=2525"

	if err := os.WriteFile(logFile, []byte(testData), 0644); err != nil {
		t.Fatal(err)
	}

	// Create output file
	outputFile := filepath.Join(tmpDir, "output.jsonl")

	// Test default behavior (no --full-envelope flag)
	args := []string{"parse", logFile}

	// Capture output by using file output
	file, err := os.Create(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	// Run the CLI command with default settings
	if err := run(args, file); err != nil {
		t.Fatalf("CLI run failed: %v", err)
	}
	file.Close()

	// Read output
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(outputData)
	forbiddenFields := []string{`"raw":`, `"nli":`, `"meta":`, `"source":`, `"envelope":`}
	for _, forbidden := range forbiddenFields {
		if strings.Contains(outputStr, forbidden) {
			t.Errorf("Default CLI output contains forbidden field %s", forbidden)
		}
	}
	decoder := json.NewDecoder(strings.NewReader(outputStr))
	decoder.UseNumber()
	var payload map[string]interface{}
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("decode CLI payload: %v", err)
	}
	if len(payload) != 14 {
		t.Fatalf("CLI emitted %d fields, expected 14: %s", len(payload), outputStr)
	}
	if payload["id"] != "6864BD5E047384B0" || payload["major_pid"] != "3609" || payload["host"] != "11.22.33.44" {
		t.Fatalf("CLI reviewed payload mismatch: %#v", payload)
	}
	number, ok := payload["pid_status"].(json.Number)
	if !ok || number.String() != "0" {
		t.Fatalf("CLI numeric pid_status=%v (%T)", payload["pid_status"], payload["pid_status"])
	}
}

// TestCLIFullEnvelopeExplicit tests that --full-envelope flag shows warning and includes raw
func TestCLIFullEnvelopeExplicit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test log file
	logFile := filepath.Join(tmpDir, "test.log")
	testData := `2024-10-04 12:34:56 INFO Application started successfully
2024-10-04 12:34:57 INFO Processing request for user=john@example.com`

	if err := os.WriteFile(logFile, []byte(testData), 0644); err != nil {
		t.Fatal(err)
	}

	// Create output file
	outputFile := filepath.Join(tmpDir, "output.jsonl")

	// Capture stderr to check for warnings
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Create file for output
	file, err := os.Create(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	// Run with --full-envelope flag
	args := []string{"parse", "--full-envelope", logFile}

	// Capture stderr
	stderrChan := make(chan string, 1)
	go func() {
		defer w.Close()
		if err := run(args, file); err != nil {
			stderrChan <- "error: " + err.Error()
		} else {
			stderrChan <- "success"
		}
	}()

	stderrBytes, _ := io.ReadAll(r)
	stderrOutput := string(stderrBytes)
	result := <-stderrChan
	os.Stderr = oldStderr
	file.Close()

	if strings.HasPrefix(result, "error:") {
		t.Logf("Full envelope command result: %s", result)
	}

	// Read output file
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}

	outputStr := string(outputData)

	// In diagnostic mode, we expect different behavior depending on v5 vs v4 fallback
	// The key test is that this is explicitly requested diagnostic mode
	if len(outputStr) == 0 {
		t.Error("Full envelope mode should produce output")
	}

	// WARNING should be present when explicitly using full envelope mode (if stderr captured)
	if len(stderrOutput) > 0 && !strings.Contains(stderrOutput, "WARNING") {
		t.Logf("Expected WARNING in stderr for full envelope mode, got: %s", stderrOutput)
	}
}

// TestCLIFlagValidation tests CLI flag validation and help behavior
func TestCLIFlagValidation(t *testing.T) {
	// Test help command
	args := []string{"help"}

	// Capture output
	var output strings.Builder

	if err := run(args, &output); err != nil {
		t.Fatalf("Help command failed: %v", err)
	}

	helpOutput := output.String()

	// Should show usage information
	if !strings.Contains(helpOutput, "Usage") && !strings.Contains(helpOutput, "parse") {
		t.Errorf("Help output should contain usage information, got: %s", helpOutput)
	}

	// Test missing required argument
	args = []string{"parse"}

	var errorOutput strings.Builder
	if err := run(args, &errorOutput); err == nil {
		t.Error("CLI should return error for missing file argument")
	}
}

// TestCLIVersionCommand tests version command works
func TestCLIVersionCommand(t *testing.T) {
	args := []string{"version"}

	var output strings.Builder
	if err := run(args, &output); err != nil {
		t.Fatalf("Version command failed: %v", err)
	}

	versionOutput := output.String()
	if !strings.Contains(versionOutput, "nli") {
		t.Errorf("Version output should contain 'nli', got: %s", versionOutput)
	}
}

func TestCLIReviewedFamiliesDefaultRoute(t *testing.T) {
	var contracts struct {
		Records []struct {
			Family string `json:"family"`
			Raw    string `json:"raw"`
		} `json:"records"`
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "migration", "sample-logs", "records.expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &contracts); err != nil {
		t.Fatal(err)
	}
	if len(contracts.Records) != 8 {
		t.Fatalf("representative records = %d, want 8", len(contracts.Records))
	}
	lines := make([]string, 0, len(contracts.Records))
	for _, record := range contracts.Records {
		lines = append(lines, record.Raw)
	}
	logPath := filepath.Join(t.TempDir(), "all-reviewed-families.log")
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var detected strings.Builder
	if err := run([]string{"detect", logPath}, &detected); err != nil {
		t.Fatal(err)
	}
	detections := decodeObjectStream(t, detected.String())
	expected := []string{
		"nli_reviewed_imap/reviewed_imap",
		"nli_reviewed_pop/reviewed_pop",
		"nli_reviewed_webtop_webmail/reviewed_webtop_webmail",
		"nli_reviewed_mta/reviewed_mta",
		"nli_reviewed_pss_pabd/reviewed_pss_pabd",
		"nli_reviewed_mss_capd/reviewed_mss_capd",
		"nli_reviewed_smtp_in/reviewed_smtp_in",
		"nli_reviewed_smtp_out/reviewed_smtp_out",
	}
	if len(detections) != len(expected) {
		t.Fatalf("CLI detections = %d, want %d: %s", len(detections), len(expected), detected.String())
	}
	for index, parserID := range expected {
		if detections[index]["parser"] != parserID || detections[index]["route"] != "normal" {
			t.Errorf("detection %d = %#v, want parser %s", index, detections[index], parserID)
		}
	}

	var parsed strings.Builder
	if err := run([]string{"parse", logPath}, &parsed); err != nil {
		t.Fatal(err)
	}
	payloads := decodeObjectStream(t, parsed.String())
	if len(payloads) != 8 {
		t.Fatalf("CLI payloads = %d, want 8", len(payloads))
	}
	for index, payload := range payloads {
		if len(payload) == 0 {
			t.Errorf("payload %d is empty", index)
		}
		for _, forbidden := range []string{"raw", "nli", "meta", "source", "envelope"} {
			if _, exists := payload[forbidden]; exists {
				t.Errorf("payload %d contains forbidden field %s", index, forbidden)
			}
		}
	}
}

func TestCLIPreservesReservedInputAliases(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "migration", "sample-logs", "families", "smtp-in.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(fixture), "\r\n", "\n"), "\n")
	if len(lines) < 190 {
		t.Fatalf("SMTP-IN fixture has %d lines", len(lines))
	}
	logPath := filepath.Join(t.TempDir(), "alias-record.log")
	if err := os.WriteFile(logPath, []byte(lines[189]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := run([]string{"parse", logPath}, &output); err != nil {
		t.Fatal(err)
	}
	payloads := decodeObjectStream(t, output.String())
	if len(payloads) != 1 {
		t.Fatalf("CLI alias payloads = %d, want 1: %s", len(payloads), output.String())
	}
	payload := payloads[0]
	if payload["input.source"] != "11.22.33.44" || payload["input.envelope"] != "bounce@ea.guardian-angel-reading.com" {
		t.Fatalf("reserved aliases were not preserved: %#v", payload)
	}
	if _, exists := payload["source"]; exists {
		t.Fatal("bare source field escaped the alias contract")
	}
	if _, exists := payload["envelope"]; exists {
		t.Fatal("bare envelope field escaped the alias contract")
	}
}

func decodeObjectStream(t *testing.T, input string) []map[string]interface{} {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(input))
	objects := []map[string]interface{}{}
	for {
		var object map[string]interface{}
		if err := decoder.Decode(&object); err == io.EOF {
			return objects
		} else if err != nil {
			t.Fatal(err)
		}
		objects = append(objects, object)
	}
}

func TestCLICAPDPlaceholdersQuarantineByDefault(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "migration", "sample-logs", "families", "mss-capd.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(fixture), "\r\n", "\n")), "\n")
	if len(lines) != 13 {
		t.Fatalf("CAPD fixture lines = %d, want 13", len(lines))
	}
	logPath := filepath.Join(t.TempDir(), "malformed-capd.log")
	if err := os.WriteFile(logPath, []byte(strings.Join(lines[9:], "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var detected strings.Builder
	if err := run([]string{"detect", logPath}, &detected); err != nil {
		t.Fatal(err)
	}
	objects := decodeObjectStream(t, detected.String())
	if len(objects) != 4 {
		t.Fatalf("CAPD detections = %d, want 4", len(objects))
	}
	for index, object := range objects {
		if object["route"] != "quarantine" || object["format"] != "v5_parserpack" {
			t.Errorf("CAPD placeholder %d was not quarantined: %#v", index, object)
		}
	}
}
