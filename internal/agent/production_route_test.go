package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexora.local/nli-v20/internal/output"
	"nexora.local/nli-v20/telemetry"
)

func TestRemoteOnlyProfileGetsMandatoryRetryableLocalQuarantinePath(t *testing.T) {
	config := Config{StateDir: t.TempDir(), Outputs: []output.Config{{Type: "clickhouse", URL: "https://clickhouse.example.com", Routes: []string{"normal", "warning"}}}}
	ensureLocalQuarantineOutput(&config)
	paths := quarantinePathsForFamily(config.Outputs, "smtp")
	if len(paths) != 1 || !strings.Contains(paths[0], filepath.Join("quarantine", "records.jsonl")) {
		t.Fatalf("remote-only configuration did not retain a local quarantine path: %#v", paths)
	}
	count := len(config.Outputs)
	ensureLocalQuarantineOutput(&config)
	if len(config.Outputs) != count {
		t.Fatal("a second catch-all quarantine output was added")
	}
}

func TestAgentExactSMTPProductionRoute(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "smtp.log")
	eventPath := filepath.Join(root, "events.jsonl")
	telemetryPath := filepath.Join(root, "telemetry.jsonl")
	stateDir := filepath.Join(root, "state")
	input := "20250812 130034198+0100 aaa-gmailrgo009 smtp 3609 0 140290550523648 Note;SmtpServerConnect(2001/3) id=6864BD5E047384B0:host=11.22.33.44:connection_count=1:local_port=2525"
	if err := os.WriteFile(inputPath, []byte(input+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Version:  2,
		Inputs:   []InputConfig{{Path: inputPath, Service: "auto"}},
		StateDir: stateDir, PollMS: 50,
		Outputs:          []output.Config{{Type: "file", Path: eventPath, Routes: []string{"normal"}}},
		TelemetryOutputs: []output.Config{{Type: "file", Path: telemetryPath, Routes: []string{"telemetry"}}},
	}
	if err := Run(context.Background(), config, true); err != nil {
		t.Fatal(err)
	}

	eventBytes, err := os.ReadFile(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(eventBytes, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 14 || payload["id"] != "6864BD5E047384B0" || payload["host"] != "11.22.33.44" {
		t.Fatalf("unexpected production EventPayload: %#v", payload)
	}
	for _, forbidden := range []string{"raw", "nli", "meta", "source", "envelope"} {
		if _, exists := payload[forbidden]; exists {
			t.Fatalf("forbidden EventPayload field %q", forbidden)
		}
	}

	telemetryBytes, err := os.ReadFile(telemetryPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(telemetryBytes), "6864BD5E047384B0") || strings.Contains(string(telemetryBytes), "11.22.33.44") {
		t.Fatalf("telemetry retained source values: %s", telemetryBytes)
	}
	var metadata telemetry.ParseTelemetry
	if err := json.Unmarshal(telemetryBytes, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.ParserID != "nli_reviewed_smtp_out/reviewed_smtp_out" || metadata.PolicyHash == "" ||
		metadata.InputFieldCount != 14 || metadata.OutputFieldCount != 14 || metadata.SourceFieldCount != 14 ||
		metadata.EvidenceCoverage != 1 || metadata.AttemptCount != 2 || metadata.StageCount != 3 ||
		metadata.Route != "normal" || metadata.Result != "success" || len(metadata.DeliveryAcks) != 1 {
		t.Fatalf("incomplete production telemetry: %+v", metadata)
	}

	if err := filepath.Walk(stateDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "6864BD5E047384B0") || strings.Contains(string(data), "11.22.33.44") || strings.Contains(string(data), `"event_json"`) {
			t.Fatalf("normal agent state retained source/event content in %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestControllerOutputRoutesOnlyAuthenticatedParserTelemetry(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "smtp.log")
	const nodeID, nodeToken = "enrolled-node-42", "node-secret-token"
	if err := os.WriteFile(inputPath, []byte("20250812 130034198+0100 aaa-gmailrgo009 smtp 3609 0 140290550523648 Note;SmtpServerConnect(2001/3) id=6864BD5E047384B0:host=11.22.33.44:connection_count=1:local_port=2525\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nodeIDPath, tokenPath := filepath.Join(root, "install.id"), filepath.Join(root, "node.token")
	if err := os.WriteFile(nodeIDPath, []byte(nodeID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte(nodeToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/telemetry" {
			t.Errorf("controller output used unsafe event endpoint %q", r.URL.Path)
			http.Error(w, "legacy events disabled", http.StatusGone)
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+nodeToken {
			t.Errorf("controller telemetry was not authenticated: method=%s authorization=%q", r.Method, r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode controller telemetry: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	config := Config{
		Version: 2, Inputs: []InputConfig{{Path: inputPath, Service: "auto"}},
		StateDir: filepath.Join(root, "state"), PollMS: 50,
		Outputs: []output.Config{{Type: "controller", URL: server.URL, NodeIDFile: nodeIDPath, TokenFile: tokenPath, Routes: []string{"normal", "warning"}}},
	}
	if err := Run(context.Background(), config, true); err != nil {
		t.Fatal(err)
	}
	if received["node_id"] != nodeID || received["route"] != "normal" || received["output_field_count"] != float64(14) {
		t.Fatalf("controller did not receive enrolled-node parse metadata: %#v", received)
	}
	for _, sourceValue := range []string{"6864BD5E047384B0", "11.22.33.44"} {
		if strings.Contains(mustJSON(t, received), sourceValue) {
			t.Fatalf("controller telemetry retained customer event value %q: %#v", sourceValue, received)
		}
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestRemoteFailureDoesNotAdvanceInputCheckpoint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input.log")
	if err := os.WriteFile(path, []byte("{\"level\":\"info\",\"message\":\"must arrive\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cfg := Config{Inputs: []InputConfig{{Path: path, Service: "auto"}}, StateDir: filepath.Join(root, "state"), PollMS: 50,
		Outputs: []output.Config{{Type: "loki", URL: server.URL, Required: true, BatchSize: 100, RetryMax: 1, RetryBackoffMS: 1}}}
	if err := Run(context.Background(), cfg, true); err == nil {
		t.Fatal("buffered remote failure reported success")
	}
	if data, err := os.ReadFile(filepath.Join(cfg.StateDir, "offsets.json")); err == nil {
		var states map[string]cursor
		if err := json.Unmarshal(data, &states); err != nil {
			t.Fatal(err)
		}
		if states[path].Offset != 0 {
			t.Fatalf("lost undelivered input: %s", data)
		}
	}
	events := filepath.Join(root, "recovered.jsonl")
	cfg.Outputs = []output.Config{{Type: "file", Path: events}}
	if err := Run(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(events)
	if err != nil || !strings.Contains(string(data), "must arrive") {
		t.Fatalf("input not replayed: %s %v", data, err)
	}
}

func TestAccountedOptionalFailureDoesNotReplaySuccessfulLocalOutput(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "input.log")
	outputPath := filepath.Join(root, "normalized.jsonl")
	spoolPath := filepath.Join(root, "loki-failures.jsonl")
	if err := os.WriteFile(inputPath, []byte("{\"level\":\"info\",\"message\":\"emit once\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cfg := Config{
		Inputs: []InputConfig{{Path: inputPath, Service: "auto"}}, StateDir: filepath.Join(root, "state"), PollMS: 50,
		DisableV5Parser: true, EnableLegacyRouting: true,
		Outputs: []output.Config{
			{Type: "file", Path: outputPath, Routes: []string{"normal", "warning"}},
			{Type: "loki", URL: server.URL, Routes: []string{"normal", "warning"}, RetryMax: 1, RetryBackoffMS: 1, FallbackPath: spoolPath},
		},
	}
	if err := Run(context.Background(), cfg, true); err != nil {
		t.Fatalf("accounted optional failure stopped ingestion: %v", err)
	}
	if err := Run(context.Background(), cfg, true); err != nil {
		t.Fatalf("restart after accounted optional failure: %v", err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; rows != 1 {
		t.Fatalf("successful local output replayed after optional sink failure: rows=%d data=%s", rows, data)
	}
	if receipt, err := os.ReadFile(spoolPath); err != nil || len(receipt) == 0 {
		t.Fatalf("optional failure receipt missing: %s %v", receipt, err)
	}
}

func TestAgentOutputInitializationErrorsAreReturned(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "input.log")
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(inputPath, []byte("INFO test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	config := Config{
		Version:  2,
		Inputs:   []InputConfig{{Path: inputPath, Service: "auto"}},
		StateDir: filepath.Join(root, "state"), PollMS: 50,
		Outputs: []output.Config{{Type: "file", Path: filepath.Join(blocker, "output.jsonl")}},
	}
	if err := Run(context.Background(), config, true); err == nil {
		t.Fatal("agent swallowed output initialization failure")
	}
}
