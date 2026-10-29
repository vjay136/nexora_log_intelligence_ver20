package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/familyprofile"
	"nexora.local/nli-v20/internal/output"
	parserpolicy "nexora.local/nli-v20/parser-policy"
	"nexora.local/nli-v20/telemetry"
)

func TestAgentStartupRoutesAllReviewedFamiliesAndCAPDQuarantine(t *testing.T) {
	var contracts struct {
		Records []struct {
			Family string `json:"family"`
			Raw    string `json:"raw"`
		} `json:"records"`
	}
	contractPath := filepath.Join("..", "..", "testdata", "migration", "sample-logs", "records.expected.json")
	data, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &contracts); err != nil {
		t.Fatal(err)
	}
	if len(contracts.Records) != 8 {
		t.Fatalf("representative records = %d, want 8", len(contracts.Records))
	}

	rawRecords := make([]string, 0, 12)
	for _, record := range contracts.Records {
		rawRecords = append(rawRecords, record.Raw)
	}
	capdPath := filepath.Join("..", "..", "testdata", "migration", "sample-logs", "families", "mss-capd.log")
	capd, err := os.Open(capdPath)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(capd)
	line := 0
	for scanner.Scan() {
		line++
		if line >= 10 {
			rawRecords = append(rawRecords, scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		capd.Close()
		t.Fatal(err)
	}
	if err := capd.Close(); err != nil {
		t.Fatal(err)
	}
	if len(rawRecords) != 12 {
		t.Fatalf("startup records = %d, want 12", len(rawRecords))
	}

	root := t.TempDir()
	inputPath := filepath.Join(root, "all-families.log")
	eventPath := filepath.Join(root, "events.jsonl")
	telemetryPath := filepath.Join(root, "telemetry.jsonl")
	stateDir := filepath.Join(root, "state")
	if err := os.WriteFile(inputPath, []byte(strings.Join(rawRecords, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Version: 2, Inputs: []InputConfig{{Path: inputPath, Service: "auto"}},
		StateDir: stateDir, PollMS: 50,
		Outputs:          []output.Config{{Type: "file", Path: eventPath, Routes: []string{"normal"}}},
		TelemetryOutputs: []output.Config{{Type: "file", Path: telemetryPath, Routes: []string{"telemetry"}}},
	}
	if err := Run(context.Background(), config, true); err != nil {
		t.Fatal(err)
	}

	events := readJSONObjectLines(t, eventPath)
	if len(events) != 8 {
		t.Fatalf("normal EventPayload records = %d, want 8", len(events))
	}
	for index, event := range events {
		if len(event) == 0 {
			t.Errorf("event %d is empty", index)
		}
		for _, forbidden := range []string{"raw", "nli", "meta", "source", "envelope"} {
			if _, exists := event[forbidden]; exists {
				t.Errorf("event %d contains forbidden field %s", index, forbidden)
			}
		}
	}

	telemetryBytes, err := os.ReadFile(telemetryPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range rawRecords {
		if strings.Contains(string(telemetryBytes), raw) {
			t.Fatal("telemetry retained a raw source record")
		}
	}
	expectedParsers := map[string]int{
		"nli_reviewed_imap/reviewed_imap":                     1,
		"nli_reviewed_pop/reviewed_pop":                       1,
		"nli_reviewed_webtop_webmail/reviewed_webtop_webmail": 1,
		"nli_reviewed_mta/reviewed_mta":                       1,
		"nli_reviewed_pss_pabd/reviewed_pss_pabd":             1,
		"nli_reviewed_mss_capd/reviewed_mss_capd":             1,
		"nli_reviewed_smtp_in/reviewed_smtp_in":               1,
		"nli_reviewed_smtp_out/reviewed_smtp_out":             1,
	}
	actualParsers := map[string]int{}
	normal, quarantined := 0, 0
	telemetryScanner := bufio.NewScanner(strings.NewReader(string(telemetryBytes)))
	for telemetryScanner.Scan() {
		var metadata telemetry.ParseTelemetry
		if err := json.Unmarshal(telemetryScanner.Bytes(), &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.Route == "quarantine" {
			quarantined++
			continue
		}
		normal++
		actualParsers[metadata.ParserID]++
		if len(metadata.PolicyHash) != 64 || metadata.AttemptCount < 1 || metadata.AttemptCount > 32 {
			t.Errorf("incomplete production telemetry: %+v", metadata)
		}
	}
	if err := telemetryScanner.Err(); err != nil {
		t.Fatal(err)
	}
	if normal != 8 || quarantined != 4 {
		t.Fatalf("telemetry routes: normal=%d quarantine=%d", normal, quarantined)
	}
	quarantinePath := filepath.Join(stateDir, "quarantine", "records.jsonl")
	quarantineData, err := os.ReadFile(quarantinePath)
	if err != nil {
		t.Fatalf("mandatory exact-raw quarantine was not created: %v", err)
	}
	for _, raw := range rawRecords[8:] {
		if !strings.Contains(string(quarantineData), raw) {
			t.Fatalf("quarantine did not retain failed source line: %s", raw)
		}
	}
	for parserID, count := range expectedParsers {
		if actualParsers[parserID] != count {
			t.Errorf("parser %s count = %d, want %d", parserID, actualParsers[parserID], count)
		}
	}

	if err := filepath.Walk(stateDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		if path == quarantinePath {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, raw := range rawRecords {
			if strings.Contains(string(data), raw) {
				t.Fatalf("agent state retained raw input in %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentStartupLoadsApprovedManagedGenerationAndRejectsTamper(t *testing.T) {
	const input = "20250812 130034198+0100 aaa-gmailrgo009 smtp 3609 0 140290550523648 Note;SmtpServerConnect(2001/3) id=6864BD5E047384B0:host=11.22.33.44:connection_count=1:local_port=2525"
	findBuiltin := func(t *testing.T) *parserpolicy.ParserPack {
		t.Helper()
		profiles, err := familyprofile.Builtins()
		if err != nil {
			t.Fatal(err)
		}
		for _, profile := range profiles {
			if profile.Pack.ID == "nli_reviewed_smtp_out" {
				data, err := json.Marshal(profile.Pack)
				if err != nil {
					t.Fatal(err)
				}
				clone, err := parserpolicy.LoadParserPackBytes(data)
				if err != nil {
					t.Fatal(err)
				}
				return clone
			}
		}
		t.Fatal("SMTP-OUT builtin not found")
		return nil
	}
	writeManaged := func(t *testing.T, stateDir string, data []byte) string {
		t.Helper()
		directory := filepath.Join(stateDir, "parsers")
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "controller-active.parserpack.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	configFor := func(root, telemetryPath string) Config {
		inputPath := filepath.Join(root, "smtp-out.log")
		if err := os.WriteFile(inputPath, []byte(input+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return Config{
			Version: 2, Inputs: []InputConfig{{Path: inputPath, Service: "auto"}},
			StateDir: filepath.Join(root, "state"), PollMS: 50,
			Outputs:          []output.Config{{Type: "discard"}},
			TelemetryOutputs: []output.Config{{Type: "file", Path: telemetryPath, Routes: []string{"telemetry"}}},
		}
	}

	t.Run("approved generation remains approved", func(t *testing.T) {
		root := t.TempDir()
		telemetryPath := filepath.Join(root, "telemetry.jsonl")
		config := configFor(root, telemetryPath)
		pack := findBuiltin(t)
		pack.ReviewState = parserpolicy.ReviewStateApproved
		pack.ActivatedAt = nil
		pack.Revision = 2
		pack.Author = "controller-approved-review"
		if err := parserpolicy.SealParserPack(pack); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.MarshalIndent(pack, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		managedPath := writeManaged(t, config.StateDir, append(encoded, '\n'))
		if err := Run(context.Background(), config, true); err != nil {
			t.Fatal(err)
		}
		telemetryRecords := readJSONObjectLines(t, telemetryPath)
		if len(telemetryRecords) != 1 || telemetryRecords[0]["policy_hash"] != pack.SHA256 || telemetryRecords[0]["pack_id"] != pack.ID {
			t.Fatalf("managed generation was not used: %#v", telemetryRecords)
		}
		persisted, err := os.ReadFile(managedPath)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := parserpolicy.LoadParserPackBytes(persisted)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.ReviewState != parserpolicy.ReviewStateApproved || loaded.ActivatedAt != nil {
			t.Fatalf("agent auto-activated the managed pack: %+v", loaded)
		}
	})

	t.Run("tampered generation fails closed", func(t *testing.T) {
		root := t.TempDir()
		config := configFor(root, filepath.Join(root, "telemetry.jsonl"))
		pack := findBuiltin(t)
		encoded, err := json.MarshalIndent(pack, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		tampered := strings.Replace(string(encoded), "nexora-parser-review-board", "tampered-parser-review-board", 1)
		writeManaged(t, config.StateDir, []byte(tampered+"\n"))
		if err := Run(context.Background(), config, true); err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
			t.Fatalf("tampered managed generation did not fail startup: %v", err)
		}
	})
}

func readJSONObjectLines(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	objects := []map[string]interface{}{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var object map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &object); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, object)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return objects
}
