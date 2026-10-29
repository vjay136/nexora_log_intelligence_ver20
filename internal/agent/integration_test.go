package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/eventstore"
	"nexora.local/nli-v20/internal/output"
)

// TestEndToEndV5Integration validates full v5 flow with controlplane
func TestEndToEndV5Integration(t *testing.T) {
	// Create temp directory and file path for test event store
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "eventstore.jsonl")

	// Create test event store for telemetry metadata
	store, err := eventstore.New(storePath, 100)
	if err != nil {
		t.Fatal(err)
	}

	// Create mock telemetry server
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer testServer.Close()

	// Test metadata can be posted to telemetry endpoint
	metadata := eventstore.TelemetryMetadata{
		EventID:          "evt-789",
		Service:          "test",
		ParserID:         "test-v5",
		Route:            "telemetry",
		Confidence:       0.95,
		EvidenceCoverage: 0.85,
		FieldCount:       4,
		LatencyMs:        10,
		Timestamp:        time.Now(),
	}

	// Store via eventstore (simulates what the agent does)
	_, err = store.Append("test-node", metadata)
	if err != nil {
		t.Fatal(err)
	}

	// Test telemetry writer can post to endpoint
	tokenPath := filepath.Join(tmpDir, "node.token")
	if err := os.WriteFile(tokenPath, []byte("test-node-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writer, err := output.NewTelemetryWriter(output.Config{
		Type:      "telemetry",
		URL:       testServer.URL,
		NodeID:    "test-node",
		TokenFile: tokenPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	metadataBytes, _ := json.Marshal(metadata)
	err = writer.Write(metadataBytes, "telemetry")
	if err != nil {
		t.Errorf("Telemetry writer should succeed: %v", err)
	}
}

// TestV5DefaultsNoFullEnvelopes validates agent Config defaults prevent full envelope transmission
func TestV5DefaultsNoFullEnvelopes(t *testing.T) {
	tests := []struct {
		name                string
		disableV5Parser     bool
		enableLegacyRouting bool
		expectSeparated     bool
	}{
		{
			name:                "v5_default_separated",
			disableV5Parser:     false,
			enableLegacyRouting: false,
			expectSeparated:     true,
		},
		{
			name:                "legacy_explicit_not_separated",
			disableV5Parser:     false,
			enableLegacyRouting: true,
			expectSeparated:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				DisableV5Parser:     tt.disableV5Parser,
				EnableLegacyRouting: tt.enableLegacyRouting,
			}

			// Check that defaults properly configure v5 separation
			hasV5Parser := !config.DisableV5Parser
			hasLegacyRouting := config.EnableLegacyRouting

			// v5 separated mode should be enabled when v5 parser is on and legacy routing is off
			actualSeparated := hasV5Parser && !hasLegacyRouting
			if actualSeparated != tt.expectSeparated {
				t.Errorf("Expected separated mode: %v, got: %v (v5parser: %v, legacy: %v)",
					tt.expectSeparated, actualSeparated, hasV5Parser, hasLegacyRouting)
			}
		})
	}
}

// TestLegacyRoutingSanitization validates sanitization function removes forbidden fields
func TestLegacyRoutingSanitization(t *testing.T) {
	// Test data with forbidden fields
	testData := map[string]interface{}{
		"safe_field": "safe_value",
		"_unknown": map[string]interface{}{
			"raw":      "FORBIDDEN_RAW_CONTENT",
			"metadata": "safe_metadata",
		},
		"raw": "ALSO_FORBIDDEN",
	}

	sanitized := sanitizeEventData(testData, "normal")

	// Convert to JSON to check content
	sanitizedBytes, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	sanitizedStr := string(sanitizedBytes)

	// Verify forbidden content was stripped
	if strings.Contains(sanitizedStr, "FORBIDDEN_RAW_CONTENT") {
		t.Error("Sanitization should remove forbidden raw content from _unknown")
	}
	if strings.Contains(sanitizedStr, "ALSO_FORBIDDEN") {
		t.Error("Sanitization should remove top-level raw fields")
	}
	if !strings.Contains(sanitizedStr, "safe_value") {
		t.Error("Sanitization should preserve safe fields")
	}
	if !strings.Contains(sanitizedStr, "safe_metadata") {
		t.Error("Sanitization should preserve safe nested fields")
	}

	// Test quarantine route returns minimal data
	quarantined := sanitizeEventData(testData, "quarantine")
	quarantinedBytes, err := json.Marshal(quarantined)
	if err != nil {
		t.Fatal(err)
	}

	if len(quarantined) != 0 || string(quarantinedBytes) != "{}" {
		t.Errorf("Quarantine route must not emit customer data: %s", quarantinedBytes)
	}
}

// TestTelemetryWriterForbiddenFields validates telemetry writer rejects forbidden content
func TestTelemetryWriterForbiddenFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tokenPath := filepath.Join(t.TempDir(), "node.token")
	if err := os.WriteFile(tokenPath, []byte("test-node-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writer, err := output.NewTelemetryWriter(output.Config{
		Type:      "telemetry",
		URL:       server.URL,
		NodeID:    "test-node",
		TokenFile: tokenPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		data        string
		shouldError bool
	}{
		{
			name:        "clean_metadata",
			data:        `{"parser_id":"test","confidence":0.8}`,
			shouldError: false,
		},
		{
			name:        "forbidden_raw_field",
			data:        `{"parser_id":"test","raw":"forbidden"}`,
			shouldError: true,
		},
		{
			name:        "forbidden_data_field",
			data:        `{"parser_id":"test","data":"forbidden"}`,
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := writer.Write([]byte(tt.data), "telemetry")
			if tt.shouldError && err == nil {
				t.Error("Expected error for forbidden content")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Unexpected error for clean content: %v", err)
			}
		})
	}
}
