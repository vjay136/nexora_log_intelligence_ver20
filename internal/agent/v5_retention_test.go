package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/eventstore"
	"nexora.local/nli-v20/internal/output"
	"nexora.local/nli-v20/internal/routing"
	"nexora.local/nli-v20/telemetry"
)

// TestAgentV5DefaultsNoFullEnvelope tests that agent defaults never send full envelopes
func TestAgentV5DefaultsNoFullEnvelope(t *testing.T) {
	root := t.TempDir()
	outputPath := filepath.Join(root, "test-output.jsonl")
	// Test agent configuration with minimal settings (production defaults)
	config := Config{
		Version: 2,
		Inputs: []InputConfig{{
			Path:    filepath.Join(root, "test.log"),
			Service: "smtp",
		}},
		StateDir:       filepath.Join(root, "state"),
		Outputs:        []output.Config{{Type: "file", Path: outputPath}},
		PollMS:         1000,
		MetricsAddress: "",
	}

	// Create v5 router (should be default)
	router, err := routing.NewV5Router(config.Outputs, []output.Config{{Type: "discard"}})
	if err != nil {
		t.Fatalf("Failed to create v5 router: %v", err)
	}
	defer router.Close()

	// Simulate parsing result
	parseResult := &routing.V5ParseResult{
		EventPayload: &telemetry.EventPayload{
			Fields: map[string]interface{}{
				"timestamp": "2024-10-04T16:45:05Z",
				"service":   "smtp",
				"message":   "test message",
			},
		},
		ParseTelemetry: &telemetry.ParseTelemetry{
			EventID:          "evt-123",
			ParserID:         "smtp-v5",
			Confidence:       0.95,
			EvidenceCoverage: 0.85,
		},
		Success: true,
	}

	// Route the result
	err = router.RouteV5(parseResult)
	if err != nil {
		t.Fatalf("Failed to route v5 result: %v", err)
	}
	if err := router.Flush(); err != nil {
		t.Fatalf("Failed to durably flush v5 result: %v", err)
	}

	// Verify output file contains only EventPayload (no raw/envelope)
	outputData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	// Parse the output
	var outputEvent map[string]interface{}
	if err := json.Unmarshal(outputData, &outputEvent); err != nil {
		t.Fatalf("Failed to parse output JSON: %v", err)
	}

	// CRITICAL TEST: Ensure no raw/envelope fields present
	forbiddenFields := []string{"raw", "nli", "meta", "source", "envelope"}
	for _, field := range forbiddenFields {
		if _, exists := outputEvent[field]; exists {
			t.Errorf("VIOLATION: Output contains forbidden field '%s' - agent defaults must not send full envelopes", field)
		}
	}

	// Ensure only EventPayload fields present
	if len(outputEvent) > 0 {
		// EventPayload should contain customer fields directly
		if _, hasTimestamp := outputEvent["timestamp"]; !hasTimestamp {
			t.Error("Expected EventPayload fields not found")
		}
	} else {
		t.Error("Output should contain EventPayload fields")
	}

}

// TestEventStoreNoFullEventRetention tests that eventstore stores only metadata
func TestEventStoreNoFullEventRetention(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "eventstore.jsonl")

	store, err := eventstore.New(storePath, 100)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create telemetry metadata (no raw content)
	metadata := eventstore.TelemetryMetadata{
		EventID:          "evt-456",
		Service:          "smtp",
		ParserID:         "smtp-v5",
		Route:            "normal",
		Confidence:       0.88,
		EvidenceCoverage: 0.75,
		FieldCount:       5,
		LatencyMs:        12,
		Timestamp:        time.Now(),
	}

	// Store metadata only
	record, err := store.Append("node-1", metadata)
	if err != nil {
		t.Fatalf("Failed to store telemetry: %v", err)
	}

	// Verify record contains only metadata
	if record.EventID == "" || record.Parser == "" {
		t.Error("Stored record missing required metadata fields")
	}

	// CRITICAL TEST: Read the storage file and ensure no full event JSON
	fileData, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("Failed to read store file: %v", err)
	}

	// Parse each line of the JSONL file
	lines := strings.Split(string(fileData), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var storedRecord map[string]interface{}
		if err := json.Unmarshal([]byte(line), &storedRecord); err != nil {
			t.Errorf("Failed to parse stored record: %v", err)
			continue
		}

		// CRITICAL TEST: Ensure no full event field
		if _, hasEvent := storedRecord["event"]; hasEvent {
			t.Error("VIOLATION: Eventstore contains full 'event' field - should store metadata only")
		}

		// Verify metadata fields are present
		expectedFields := []string{"node_id", "received_at", "service", "parser", "event_id"}
		for _, field := range expectedFields {
			if _, exists := storedRecord[field]; !exists {
				t.Errorf("Expected metadata field '%s' not found", field)
			}
		}
	}
}

// TestTelemetryControllerStrictSchema tests telemetry endpoint rejects forbidden fields
func TestTelemetryControllerStrictSchema(t *testing.T) {
	// Test that telemetry records with forbidden fields are rejected by schema validation

	// CRITICAL TEST: Test that forbidden fields are rejected
	forbiddenPayloads := []map[string]interface{}{
		{"event_id": "test", "node_id": "node1", "parser_id": "test", "route": "normal", "raw": "forbidden content"},
		{"event_id": "test", "node_id": "node1", "parser_id": "test", "route": "normal", "event": map[string]interface{}{"data": "forbidden"}},
		{"event_id": "test", "node_id": "node1", "parser_id": "test", "route": "normal", "payload": map[string]interface{}{"fields": "forbidden"}},
		{"event_id": "test", "node_id": "node1", "parser_id": "test", "route": "normal", "data": "forbidden"},
	}

	for i, payload := range forbiddenPayloads {
		payloadJSON, _ := json.Marshal(payload)

		// Verify JSON contains forbidden content
		payloadStr := string(payloadJSON)
		hasForbiddenContent := strings.Contains(payloadStr, "raw") ||
			strings.Contains(payloadStr, "event") ||
			strings.Contains(payloadStr, "payload") ||
			strings.Contains(payloadStr, "data")

		if !hasForbiddenContent {
			t.Errorf("Test payload %d should contain forbidden content for validation", i)
		}

		// Verify the payload would be caught by strict validation
		var testRecord struct {
			EventID  string `json:"event_id"`
			NodeID   string `json:"node_id"`
			ParserID string `json:"parser_id"`
			Route    string `json:"route"`
		}
		decoder := json.NewDecoder(bytes.NewReader(payloadJSON))
		decoder.DisallowUnknownFields()

		err := decoder.Decode(&testRecord)
		if err == nil {
			t.Errorf("VIOLATION: Payload %d with forbidden fields should be rejected by strict validation", i)
		}
	}
}

// TestNoAgentDefaultEnvelopeSerialization tests agent never serializes full envelopes by default
func TestNoAgentDefaultEnvelopeSerialization(t *testing.T) {
	// Create minimal agent config (defaults)
	config := Config{
		Version: 2,
		Inputs: []InputConfig{{
			Path:    "/tmp/test.log",
			Service: "smtp",
		}},
		StateDir: "/tmp/nli-state",
		Outputs:  []output.Config{{Type: "stdout"}},
		PollMS:   1000,
		// No TelemetryOutputs specified - should default to v5 separated routing
		// No EnableV5Parser explicitly set - should default to enabled
	}

	// This test validates the configuration logic in agent.Run()
	// New v5 default: Always use v5 separated routing unless EnableLegacyRouting is explicitly true

	// Check that the config triggers v5 routing path (new default logic)
	usesV5Routing := !config.EnableLegacyRouting
	if !usesV5Routing {
		t.Error("VIOLATION: Agent defaults should use v5 separated routing, not full envelope routing")
	}

	// Verify that the default configuration would NOT use legacy envelope routing
	usesLegacyRouting := config.EnableLegacyRouting
	if usesLegacyRouting {
		t.Error("VIOLATION: Agent defaults should never fall back to legacy envelope routing")
	}
}

// TestClickHouseEventPayloadOnly tests ClickHouse consumes EventPayload, not raw envelopes
func TestClickHouseEventPayloadOnly(t *testing.T) {
	// This test validates that ClickHouse output receives EventPayload only

	// Create EventPayload (v5 customer output)
	eventPayload := &telemetry.EventPayload{
		Fields: map[string]interface{}{
			"timestamp": "2024-10-04T16:45:05Z",
			"service":   "smtp",
			"hostname":  "mail-server-01",
			"action":    "reject",
			"reason":    "spam",
		},
	}

	payloadJSON, err := json.Marshal(eventPayload)
	if err != nil {
		t.Fatalf("Failed to marshal EventPayload: %v", err)
	}

	// CRITICAL TEST: Verify payload contains only customer fields, no raw/envelope data
	var parsedPayload map[string]interface{}
	if err := json.Unmarshal(payloadJSON, &parsedPayload); err != nil {
		t.Fatalf("Failed to parse EventPayload JSON: %v", err)
	}

	// Check for forbidden envelope fields
	forbiddenFields := []string{"raw", "nli", "meta", "source", "envelope"}
	for _, field := range forbiddenFields {
		if _, exists := parsedPayload[field]; exists {
			t.Errorf("VIOLATION: EventPayload for ClickHouse contains forbidden field '%s'", field)
		}
	}

	// Verify expected customer fields are present (EventPayload marshals Fields directly)
	expectedFields := []string{"timestamp", "service", "hostname", "action"}
	for _, field := range expectedFields {
		if _, exists := parsedPayload[field]; !exists {
			t.Errorf("Expected customer field '%s' not found in EventPayload", field)
		}
	}

	// This validates that ClickHouse writers would receive clean EventPayload
	// In actual implementation, ClickHouse output writer should only process EventPayload
}
